// Package files is the volume file API client, at the host the cloud API names.
// Tokens come from the cloud API and are renewed before they expire. Uploads and
// downloads resume after a dropped connection, and an upload interrupted in an
// earlier run resumes in the next one.
package files

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"iter"
	"math"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"featherless.ai/featherless-cloud/internal/api"
	"featherless.ai/featherless-cloud/internal/config"
	"featherless.ai/featherless-cloud/internal/errs"
	"featherless.ai/featherless-cloud/internal/version"
)

type EntryType string

const (
	TypeFile      EntryType = "file"
	TypeDirectory EntryType = "directory"
)

// FileEntry is a file or folder in a volume, in the API's field order.
type FileEntry struct {
	Name       string    `json:"name"`
	Path       string    `json:"path"`
	Type       EntryType `json:"type"`
	SizeBytes  *int64    `json:"sizeBytes,omitzero"`
	ModifiedAt string    `json:"modifiedAt"`
	Version    string    `json:"version,omitempty"`
}

// Size is the entry's size, 0 when the API left it out.
func (e FileEntry) Size() int64 {
	if e.SizeBytes == nil {
		return 0
	}

	return *e.SizeBytes
}

// FindFilters narrow a find. Sizes and times are exclusive bounds, and a size
// bound leaves out folders. Nil and empty values send nothing.
type FindFilters struct {
	Name           string
	IgnoreCase     bool
	Type           EntryType
	MaxDepth       *int
	MinSize        *int64
	MaxSize        *int64
	ModifiedAfter  *time.Time
	ModifiedBefore *time.Time
}

type UploadMode string

const (
	// ModeCreate refuses to replace anything.
	ModeCreate UploadMode = "create"
	// ModeOverwrite replaces a file.
	ModeOverwrite UploadMode = "overwrite"
)

type UploadOptions struct {
	Mode    UploadMode
	Parents bool
	// IfMatch is the remote entry's version an overwrite must still find; the
	// upload refuses with precondition_failed otherwise.
	IfMatch string
	// OnProgress gets the bytes sent so far, one call at a time.
	OnProgress func(sent int64)
}

type CopyOptions struct {
	Overwrite bool
	// Move deletes the source once it is copied.
	Move bool
}

// FileAPIError is a refusal from the file API, with its machine code. It is a
// CLI error: errs.IsCLI reports true for it.
type FileAPIError struct {
	Message string
	Status  int
	Code    string
}

func (e *FileAPIError) Error() string {
	return e.Message
}

func (e *FileAPIError) Unwrap() error {
	return &errs.Error{Kind: errs.KindCLI, Message: e.Message}
}

// SessionSource issues file sessions; api.Cloud is one.
type SessionSource interface {
	CreateFileSession(ctx context.Context, volumeID string) (api.FileSession, error)
}

// Options are test seams; zero values take the defaults.
type Options struct {
	// Delay is how long to wait before retry number attempt. retryAfter is the
	// server's Retry-After, nil when it sent none.
	Delay func(attempt int, retryAfter *time.Duration) time.Duration
	// StateFile remembers unfinished uploads between runs.
	StateFile string
	// ChunkBytes is the most one PATCH carries.
	ChunkBytes int64
	// Stall is how long an upload request may make no progress.
	Stall time.Duration
	// Now is the clock for token expiry, the stall watchdog and the upload state.
	Now func() time.Time
}

// Ask for a new token this long before the current one expires.
const refreshMargin = time.Minute

const (
	// Files up to this size go in the request that creates the upload.
	inlineUploadBytes = 4 << 20
	// Each PATCH body; Cloudflare refuses request bodies over 100 MB.
	uploadChunkBytes = 64 << 20
	// A larger file goes as up to this many lanes at once, partial uploads the server joins.
	laneCount      = 4
	lanePieceBytes = 16 << 20
	// An upload request whose bytes stop moving this long is given up and resumed, as after a dropped connection.
	stallTimeout   = 30 * time.Second
	uploadStateTTL = 24 * time.Hour
	stateFileName  = "featherless-cloud-uploads.json"
)

var retryDelays = []time.Duration{1 * time.Second, 2 * time.Second, 5 * time.Second, 10 * time.Second, 20 * time.Second, 30 * time.Second}

var retryableStatuses = map[int]bool{423: true, 429: true, 500: true, 502: true, 503: true, 504: true}

// Seams tests replace.
var (
	rename  = os.Rename
	readDir = os.ReadDir
)

// Milliseconds is t as Node's stat mtimeMs gives it: milliseconds since the
// epoch with their fraction. Upload state keys hold it as Node prints it.
func Milliseconds(t time.Time) float64 {
	return float64(t.Unix())*1000 + float64(t.Nanosecond())/1e6
}

// FormatMilliseconds prints a millisecond count as JavaScript prints numbers.
func FormatMilliseconds(milliseconds float64) string {
	return strconv.FormatFloat(milliseconds, 'f', -1, 64)
}

// ISOTime formats t as JavaScript's toISOString does.
func ISOTime(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}

// ParseTime reads an ISO 8601 time, as the file API writes them, in whole
// milliseconds; false where Date.parse gives NaN.
func ParseTime(value string) (int64, bool) {
	parsed, err := time.Parse(time.RFC3339Nano, value)

	if err != nil {
		return 0, false
	}

	return parsed.UnixMilli(), true
}

// statTime is a file's modification time as Node's Stats.mtime holds it:
// rounded to the nearest millisecond.
func statTime(modified time.Time) time.Time {
	rounded := math.Floor(Milliseconds(modified) + 0.5)
	return time.UnixMilli(int64(rounded))
}

// Client is the file API for one volume. It is safe for concurrent use.
type Client struct {
	cloud      SessionSource
	volumeID   string
	delay      func(attempt int, retryAfter *time.Duration) time.Duration
	stateFile  string
	chunkBytes int64
	stall      time.Duration
	now        func() time.Time
	http       *http.Client

	sessionMu sync.Mutex
	session   *api.FileSession
	pending   *sessionCall
	// Uploads running at once take turns saving the state file.
	stateMu sync.Mutex
	// The partial downloads an earlier run left, read once per folder.
	partialsMu sync.Mutex
	partials   map[string]*partialSet
}

type sessionCall struct {
	done    chan struct{}
	session api.FileSession
	err     error
}

type partialSet struct {
	once  sync.Once
	mu    sync.Mutex
	names map[string]bool
	err   error
}

func NewClient(cloud SessionSource, volumeID string, options Options) *Client {
	client := &Client{
		cloud:      cloud,
		volumeID:   volumeID,
		delay:      options.Delay,
		stateFile:  options.StateFile,
		chunkBytes: options.ChunkBytes,
		stall:      options.Stall,
		now:        options.Now,
		http:       &http.Client{},
		partials:   map[string]*partialSet{},
	}

	if client.delay == nil {
		client.delay = defaultDelay
	}

	if client.stateFile == "" {
		client.stateFile = filepath.Join(config.Dir(), stateFileName)
	}

	if client.chunkBytes == 0 {
		client.chunkBytes = uploadChunkBytes
	}

	if client.stall == 0 {
		client.stall = stallTimeout
	}

	if client.now == nil {
		client.now = time.Now
	}

	return client
}

func defaultDelay(attempt int, retryAfter *time.Duration) time.Duration {
	if retryAfter != nil {
		return min(*retryAfter, time.Minute)
	}

	if attempt < len(retryDelays) {
		return retryDelays[attempt]
	}

	return 30 * time.Second
}

func (c *Client) VolumeID() string {
	return c.volumeID
}

func sleep(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// current is the session to use; refresh asks for a new one. Callers that ask
// at once share one request.
func (c *Client) current(ctx context.Context, refresh bool) (api.FileSession, error) {
	c.sessionMu.Lock()

	if !refresh && c.session != nil && c.isFresh(*c.session) {
		session := *c.session
		c.sessionMu.Unlock()

		return session, nil
	}

	call := c.pending
	isAsking := call == nil

	if isAsking {
		call = &sessionCall{done: make(chan struct{})}
		c.pending = call
	}

	c.sessionMu.Unlock()

	if isAsking {
		call.session, call.err = c.cloud.CreateFileSession(ctx, c.volumeID)

		c.sessionMu.Lock()

		if call.err == nil {
			created := call.session
			c.session = &created
		}

		c.pending = nil
		c.sessionMu.Unlock()
		close(call.done)
	}

	select {
	case <-call.done:
		return call.session, call.err
	case <-ctx.Done():
		return api.FileSession{}, ctx.Err()
	}
}

func (c *Client) isFresh(session api.FileSession) bool {
	expires, err := time.Parse(time.RFC3339Nano, session.ExpiresAt)

	if err != nil {
		return false
	}

	return expires.Sub(c.now()) > refreshMargin
}

// request is one call to the file API. body makes the body anew for each
// attempt; nil sends none.
type request struct {
	method string
	header map[string]string
	body   func() (io.Reader, int64, error)
}

func bytesBody(content []byte) func() (io.Reader, int64, error) {
	return func() (io.Reader, int64, error) {
		return bytes.NewReader(content), int64(len(content)), nil
	}
}

func jsonRequest(method string, value any) request {
	content, _ := json.Marshal(value)
	return request{method: method, header: map[string]string{"Content-Type": "application/json"}, body: bytesBody(content)}
}

// peek reads a small error body and puts it back for the caller.
func peek(response *http.Response) string {
	content, _ := io.ReadAll(response.Body)
	response.Body.Close()
	response.Body = io.NopCloser(bytes.NewReader(content))

	return string(content)
}

// once sends one request with a current token; target is a path on the file
// host or an absolute URL.
func (c *Client) once(ctx context.Context, target string, call request, refresh bool) (*http.Response, error) {
	session, err := c.current(ctx, refresh)

	if err != nil {
		return nil, err
	}

	address := target

	if !strings.HasPrefix(target, "http") {
		address = session.BaseURL + target
	}

	var body io.Reader
	length := int64(0)

	if call.body != nil {
		body, length, err = call.body()

		if err != nil {
			return nil, err
		}
	}

	httpRequest, err := http.NewRequestWithContext(ctx, call.method, address, body)

	if err != nil {
		return nil, err
	}

	if call.body != nil {
		httpRequest.ContentLength = length
	}

	for name, value := range call.header {
		httpRequest.Header.Set(name, value)
	}

	httpRequest.Header.Set("Authorization", "Bearer "+session.Token)
	httpRequest.Header.Set("User-Agent", "featherless-cloud/"+version.Version)
	httpRequest.Header.Set("Tus-Resumable", "1.0.0")
	response, err := c.http.Do(httpRequest)

	if err != nil {
		return nil, err
	}

	if response.StatusCode == http.StatusUnauthorized && !refresh && strings.Contains(peek(response), "token_expired") {
		response.Body.Close()
		return c.once(ctx, target, call, true)
	}

	// The file server didn't start in time. A new session makes sure it exists; the caller's
	// retry then goes out with that session.
	if response.StatusCode == http.StatusServiceUnavailable && strings.Contains(peek(response), "files_starting") {
		if _, err := c.current(ctx, true); err != nil {
			response.Body.Close()
			return nil, err
		}
	}

	return response, nil
}

func retryAfter(response *http.Response) *time.Duration {
	if response == nil {
		return nil
	}

	seconds, err := strconv.ParseFloat(response.Header.Get("Retry-After"), 64)

	if err != nil {
		return nil
	}

	wait := time.Duration(seconds * float64(time.Second))

	return &wait
}

func refusal(response *http.Response) *FileAPIError {
	content, _ := io.ReadAll(response.Body)
	response.Body.Close()
	var parsed struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}

	json.Unmarshal(content, &parsed)
	message := parsed.Error.Message

	if message == "" {
		message = "File request failed (" + strconv.Itoa(response.StatusCode) + ")."
	}

	return &FileAPIError{Message: message, Status: response.StatusCode, Code: parsed.Error.Code}
}

func discard(response *http.Response) {
	if response != nil {
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
	}
}

// send makes a replayable request, retried on dropped connections and busy answers.
func (c *Client) send(ctx context.Context, target string, call request) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		response, err := c.once(ctx, target, call, false)

		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}

			if attempt >= len(retryDelays) {
				detail := err.Error()
				var transport *url.Error

				// Node's fetch says only this about any failure to connect.
				if errors.As(err, &transport) {
					detail = "fetch failed"
				}

				return nil, errs.New("Could not reach the file service: " + detail)
			}
		}

		if response != nil && response.StatusCode >= 200 && response.StatusCode < 300 {
			return response, nil
		}

		if response != nil && (!retryableStatuses[response.StatusCode] || attempt >= len(retryDelays)) {
			return nil, refusal(response)
		}

		wait := c.delay(attempt, retryAfter(response))
		discard(response)

		if err := sleep(ctx, wait); err != nil {
			return nil, err
		}
	}
}

// route is a path below this volume with a query in the order given; empty values send nothing.
func (c *Client) route(suffix string, query ...string) string {
	var pairs []string

	for i := 0; i+1 < len(query); i += 2 {
		if query[i+1] != "" {
			pairs = append(pairs, url.QueryEscape(query[i])+"="+url.QueryEscape(query[i+1]))
		}
	}

	target := "/v1/volumes/" + c.volumeID + "/" + suffix

	if len(pairs) > 0 {
		target += "?" + strings.Join(pairs, "&")
	}

	return target
}

var errUnexpected = errs.New("Unexpected response from the file service.")

func parseEntry(data []byte) (FileEntry, error) {
	var entry FileEntry

	if err := json.Unmarshal(data, &entry); err != nil {
		return FileEntry{}, errUnexpected
	}

	if entry.Type != TypeFile && entry.Type != TypeDirectory {
		return FileEntry{}, errUnexpected
	}

	return entry, nil
}

func readEntry(response *http.Response) (FileEntry, error) {
	defer response.Body.Close()
	content, err := io.ReadAll(response.Body)

	if err != nil {
		return FileEntry{}, err
	}

	return parseEntry(content)
}

// List returns every entry of a folder, following pages.
func (c *Client) List(ctx context.Context, folder string) ([]FileEntry, error) {
	entries := []FileEntry{}
	cursor := ""

	for {
		response, err := c.send(ctx, c.route("files", "path", folder, "cursor", cursor), request{method: http.MethodGet})

		if err != nil {
			return nil, err
		}

		content, err := io.ReadAll(response.Body)
		response.Body.Close()

		if err != nil {
			return nil, err
		}

		var page struct {
			Entries    []json.RawMessage `json:"entries"`
			NextCursor string            `json:"nextCursor"`
		}

		if err := json.Unmarshal(content, &page); err != nil || page.Entries == nil {
			return nil, errUnexpected
		}

		for _, raw := range page.Entries {
			entry, err := parseEntry(raw)

			if err != nil {
				return nil, err
			}

			entries = append(entries, entry)
		}

		cursor = page.NextCursor

		if cursor == "" {
			return entries, nil
		}
	}
}

// findEnd is how a find page ended: done, or stopped at a bound with a cursor
// that continues the walk.
type findEnd struct {
	done   bool
	cursor string
}

// Find yields every file and folder below a folder that the filters let
// through, in path order, as the file server finds them, then an error if the
// search failed. The server stops one walk at an entry or time bound, and Find
// pages on with the cursor it ends the page with.
//
// A page whose stream ends before the server says it's done fails rather than
// pass for every match; it isn't retried, since starting over would repeat
// what was already yielded. A page the server stops without a cursor to
// continue from is retried a few times before giving up.
func (c *Client) Find(ctx context.Context, folder string, filters FindFilters) iter.Seq2[FileEntry, error] {
	return func(yield func(FileEntry, error) bool) {
		cursor := ""
		stalls := 0

		for {
			end, isStopped, err := c.findPage(ctx, folder, filters, cursor, yield)

			if isStopped {
				return
			}

			if err != nil {
				yield(FileEntry{}, err)
				return
			}

			if end.done {
				return
			}

			if end.cursor == "" || end.cursor == cursor {
				stalls++

				if stalls > 2 {
					yield(FileEntry{}, errs.New("The search kept stopping at the file server's bounds before it finished. Narrow it and run it again."))
					return
				}

				continue
			}

			cursor = end.cursor
			stalls = 0
		}
	}
}

func optionalNumber[T int | int64](value *T) string {
	if value == nil {
		return ""
	}

	return strconv.FormatInt(int64(*value), 10)
}

func optionalTime(value *time.Time) string {
	if value == nil {
		return ""
	}

	return ISOTime(*value)
}

// findPage streams one page of a find, ended by its done line. isStopped
// reports that the caller stopped reading.
func (c *Client) findPage(ctx context.Context, folder string, filters FindFilters, cursor string, yield func(FileEntry, error) bool) (end findEnd, isStopped bool, err error) {
	ignoreCase := ""

	if filters.IgnoreCase {
		ignoreCase = "true"
	}

	target := c.route("find",
		"path", folder,
		"name", filters.Name,
		"ignoreCase", ignoreCase,
		"type", string(filters.Type),
		"maxDepth", optionalNumber(filters.MaxDepth),
		"minSize", optionalNumber(filters.MinSize),
		"maxSize", optionalNumber(filters.MaxSize),
		"modifiedAfter", optionalTime(filters.ModifiedAfter),
		"modifiedBefore", optionalTime(filters.ModifiedBefore),
		"cursor", cursor,
	)

	response, err := c.send(ctx, target, request{method: http.MethodGet})

	if err != nil {
		return findEnd{}, false, err
	}

	defer response.Body.Close()
	cut := errs.New("The search stopped before it finished. Run it again.")
	reader := bufio.NewReader(response.Body)

	for {
		line, readErr := reader.ReadString('\n')

		if readErr != nil {
			// A last line without its newline is not a whole line.
			if ctx.Err() != nil {
				return findEnd{}, false, ctx.Err()
			}

			return findEnd{}, false, cut
		}

		line = strings.TrimSpace(line)

		if line == "" {
			continue
		}

		var parsed struct {
			Done   *bool  `json:"done"`
			Cursor string `json:"cursor"`
			Error  *struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}

		if err := json.Unmarshal([]byte(line), &parsed); err != nil {
			return findEnd{}, false, errUnexpected
		}

		if parsed.Error != nil {
			message := parsed.Error.Message

			if message == "" {
				message = "The search failed."
			}

			return findEnd{}, false, &FileAPIError{Message: message, Status: response.StatusCode, Code: parsed.Error.Code}
		}

		if parsed.Done != nil {
			return findEnd{done: *parsed.Done, cursor: parsed.Cursor}, false, nil
		}

		entry, err := parseEntry([]byte(line))

		if err != nil {
			return findEnd{}, false, err
		}

		if !yield(entry, nil) {
			return findEnd{}, true, nil
		}
	}
}

// Entry returns the entry at a path, or nil when nothing is there.
func (c *Client) Entry(ctx context.Context, target string) (*FileEntry, error) {
	response, err := c.send(ctx, c.route("entry", "path", target), request{method: http.MethodGet})

	if err != nil {
		var refused *FileAPIError

		if errors.As(err, &refused) && refused.Status == http.StatusNotFound && refused.Code == "not_found" {
			return nil, nil
		}

		return nil, err
	}

	entry, err := readEntry(response)

	if err != nil {
		return nil, err
	}

	return &entry, nil
}

func (c *Client) Mkdir(ctx context.Context, target string, parents bool) (FileEntry, error) {
	body := struct {
		Path    string `json:"path"`
		Parents bool   `json:"parents"`
	}{target, parents}

	response, err := c.send(ctx, c.route("directories"), jsonRequest(http.MethodPost, body))

	if err != nil {
		return FileEntry{}, err
	}

	return readEntry(response)
}

func (c *Client) Remove(ctx context.Context, target string, recursive bool) error {
	isRecursive := ""

	if recursive {
		isRecursive = "true"
	}

	response, err := c.send(ctx, c.route("entry", "path", target, "recursive", isRecursive), request{method: http.MethodDelete})

	if err != nil {
		return err
	}

	discard(response)

	return nil
}

func (c *Client) Move(ctx context.Context, from, to string, overwrite bool) (FileEntry, error) {
	body := struct {
		From      string `json:"from"`
		To        string `json:"to"`
		Overwrite bool   `json:"overwrite"`
	}{from, to, overwrite}

	response, err := c.send(ctx, c.route("move"), jsonRequest(http.MethodPost, body))

	if err != nil {
		return FileEntry{}, err
	}

	return readEntry(response)
}

// CopyFrom copies, or moves, a file from a volume of the same organization
// into this one on the file server, creating missing folders. A long copy
// answers at once and sends spaces until its result.
func (c *Client) CopyFrom(ctx context.Context, fromVolume, from, to string, options CopyOptions) (FileEntry, error) {
	body := struct {
		FromVolume string `json:"fromVolume"`
		From       string `json:"from"`
		To         string `json:"to"`
		Overwrite  bool   `json:"overwrite"`
		Parents    bool   `json:"parents"`
	}{fromVolume, from, to, options.Overwrite, true}

	suffix := "copy"

	if options.Move {
		suffix = "move"
	}

	response, err := c.send(ctx, c.route(suffix), jsonRequest(http.MethodPost, body))

	if err != nil {
		return FileEntry{}, err
	}

	defer response.Body.Close()
	content, err := io.ReadAll(response.Body)

	if err != nil {
		return FileEntry{}, err
	}

	content = bytes.TrimSpace(content)
	var result struct {
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}

	if err := json.Unmarshal(content, &result); err != nil {
		return FileEntry{}, errUnexpected
	}

	if result.Error != nil {
		message := result.Error.Message

		if message == "" {
			message = "The copy failed."
		}

		return FileEntry{}, &FileAPIError{Message: message, Status: response.StatusCode, Code: result.Error.Code}
	}

	return parseEntry(content)
}

// partialsIn is the partial downloads an earlier run left in a folder, made
// and read once per folder: reading a folder of 50,000 files for every file
// downloaded into it cost 18 ms each.
func (c *Client) partialsIn(folder string) *partialSet {
	c.partialsMu.Lock()
	found := c.partials[folder]

	if found == nil {
		found = &partialSet{}
		c.partials[folder] = found
	}

	c.partialsMu.Unlock()

	found.once.Do(func() {
		found.names = map[string]bool{}

		if err := os.MkdirAll(folder, 0o777); err != nil {
			found.err = err
			return
		}

		children, err := readDir(folder)

		if err != nil {
			found.err = err
			return
		}

		for _, child := range children {
			if strings.HasSuffix(child.Name(), ".partial") {
				found.names[child.Name()] = true
			}
		}
	})

	return found
}

// errEndedEarly is a download whose body stopped short; it is retried.
var errEndedEarly = errors.New("The download ended early.")

// Download writes one file to local through `<local>.<version>.partial`, so an
// interrupted download resumes in the next run and local only ever holds a
// whole file. The remote modification time is kept.
func (c *Client) Download(ctx context.Context, remote FileEntry, local string, onProgress func(received int64)) error {
	if remote.Type != TypeFile || remote.Version == "" {
		return errs.Newf("%s is not a file.", remote.Path)
	}

	progress := onProgress

	if progress == nil {
		progress = func(int64) {}
	}

	version := remote.Version[:min(12, len(remote.Version))]
	partial := local + "." + version + ".partial"
	folder := filepath.Dir(local)
	leftovers := c.partialsIn(folder)

	if leftovers.err != nil {
		return leftovers.err
	}

	// Partial downloads of other versions of this file can't be resumed.
	leftovers.mu.Lock()

	for name := range leftovers.names {
		if strings.HasPrefix(name, filepath.Base(local)+".") && filepath.Join(folder, name) != partial {
			if err := os.Remove(filepath.Join(folder, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
				leftovers.mu.Unlock()
				return err
			}

			delete(leftovers.names, name)
		}
	}

	leftovers.mu.Unlock()

	for attempt := 0; ; attempt++ {
		err := c.downloadOnce(ctx, remote, local, partial, progress)

		if err == nil {
			return nil
		}

		var refused *FileAPIError

		if errors.As(err, &refused) && refused.Status == http.StatusRequestedRangeNotSatisfiable {
			if err := os.Remove(partial); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}

			continue
		}

		if ctx.Err() != nil {
			return ctx.Err()
		}

		if errs.IsCLI(err) || attempt >= len(retryDelays) {
			return err
		}

		if err := sleep(ctx, c.delay(attempt, nil)); err != nil {
			return err
		}
	}
}

func (c *Client) downloadOnce(ctx context.Context, remote FileEntry, local, partial string, progress func(int64)) error {
	have := int64(0)

	if info, err := os.Stat(partial); err == nil {
		have = info.Size()
	}

	call := request{method: http.MethodGet}

	if have > 0 {
		call.header = map[string]string{"Range": "bytes=" + strconv.FormatInt(have, 10) + "-", "If-Range": `"` + remote.Version + `"`}
	}

	response, err := c.send(ctx, c.route("content", "path", remote.Path), call)

	if err != nil {
		return err
	}

	defer response.Body.Close()

	// A whole response means the file changed since it was listed: start over.
	isAppend := have > 0 && response.StatusCode == http.StatusPartialContent
	base := int64(0)
	flags := os.O_CREATE | os.O_WRONLY | os.O_TRUNC

	if isAppend {
		base = have
		flags = os.O_CREATE | os.O_WRONLY | os.O_APPEND
	}

	length, lengthErr := strconv.ParseInt(response.Header.Get("Content-Length"), 10, 64)
	file, err := os.OpenFile(partial, flags, 0o666)

	if err != nil {
		return err
	}

	counted := &countingReader{reader: response.Body, onRead: func(count int64) { progress(base + count) }}
	_, copyErr := io.Copy(file, counted)
	closeErr := file.Close()

	if copyErr != nil {
		return copyErr
	}

	if closeErr != nil {
		return closeErr
	}

	info, err := os.Stat(partial)

	if err != nil {
		return err
	}

	if lengthErr == nil && info.Size() < base+length {
		return errEndedEarly
	}

	if err := rename(partial, local); err != nil {
		return err
	}

	if modified, ok := ParseTime(remote.ModifiedAt); ok {
		at := time.UnixMilli(modified)

		if err := os.Chtimes(local, at, at); err != nil {
			return err
		}
	}

	return nil
}

// countingReader passes bytes through, reporting how many have gone by.
type countingReader struct {
	reader io.Reader
	count  int64
	onRead func(count int64)
}

func (r *countingReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)

	if n > 0 {
		r.count += int64(n)
		r.onRead(r.count)
	}

	return n, err
}
