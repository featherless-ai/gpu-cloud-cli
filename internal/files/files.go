// Package files is the volume file API client, at the host the cloud API names.
// Tokens come from the cloud API and are renewed before they expire. Uploads and
// downloads resume after a dropped connection, and an upload interrupted in an
// earlier run resumes in the next one.
package files

import (
	"context"
	"iter"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"featherless.ai/featherless-cloud/internal/api"
	"featherless.ai/featherless-cloud/internal/config"
	"featherless.ai/featherless-cloud/internal/errs"
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
	// Uploads running at once take turns saving the state file.
	stateMu sync.Mutex
	// The partial downloads an earlier run left, read once per folder.
	partialsMu sync.Mutex
	partials   map[string]*partialSet
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

// List returns every entry of a folder, following pages.
func (c *Client) List(ctx context.Context, folder string) ([]FileEntry, error) {
	return nil, errs.New("not implemented")
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
		yield(FileEntry{}, errs.New("not implemented"))
	}
}

// Entry returns the entry at a path, or nil when nothing is there.
func (c *Client) Entry(ctx context.Context, target string) (*FileEntry, error) {
	return nil, errs.New("not implemented")
}

func (c *Client) Mkdir(ctx context.Context, target string, parents bool) (FileEntry, error) {
	return FileEntry{}, errs.New("not implemented")
}

func (c *Client) Remove(ctx context.Context, target string, recursive bool) error {
	return errs.New("not implemented")
}

func (c *Client) Move(ctx context.Context, from, to string, overwrite bool) (FileEntry, error) {
	return FileEntry{}, errs.New("not implemented")
}

// CopyFrom copies, or moves, a file from a volume of the same organization
// into this one on the file server, creating missing folders. A long copy
// answers at once and sends spaces until its result.
func (c *Client) CopyFrom(ctx context.Context, fromVolume, from, to string, options CopyOptions) (FileEntry, error) {
	return FileEntry{}, errs.New("not implemented")
}

// Upload sends one local file to remote, keeping its modification time.
func (c *Client) Upload(ctx context.Context, local, remote string, options UploadOptions) error {
	return errs.New("not implemented")
}

// Download writes one file to local through `<local>.<version>.partial`, so an
// interrupted download resumes in the next run and local only ever holds a
// whole file. The remote modification time is kept.
func (c *Client) Download(ctx context.Context, remote FileEntry, local string, onProgress func(received int64)) error {
	return errs.New("not implemented")
}
