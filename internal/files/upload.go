package files

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"featherless.ai/featherless-cloud/internal/errs"
)

// savedUpload is one unfinished upload in the state file. A lane upload keeps its lanes apart
// from url, so a CLI from before lanes starts over rather than resume a lane as the file.
type savedUpload struct {
	URL   string   `json:"url,omitempty"`
	Lanes []string `json:"lanes,omitempty"`
	At    *float64 `json:"at"`
}

// metadataPair is one Upload-Metadata entry; the header keeps their order.
type metadataPair struct {
	key   string
	value string
}

func encodeMetadata(pairs ...metadataPair) string {
	encoded := make([]string, 0, len(pairs))

	for _, pair := range pairs {
		encoded = append(encoded, pair.key+" "+base64.StdEncoding.EncodeToString([]byte(pair.value)))
	}

	return strings.Join(encoded, ",")
}

// uploadState is the unfinished uploads saved less than a day ago.
func (c *Client) uploadState() map[string]savedUpload {
	state := map[string]savedUpload{}
	content, err := os.ReadFile(c.stateFile)

	if err != nil {
		return state
	}

	var all map[string]json.RawMessage

	if err := json.Unmarshal(content, &all); err != nil {
		return state
	}

	now := float64(c.now().UnixMilli())

	for key, raw := range all {
		var saved savedUpload

		if err := json.Unmarshal(raw, &saved); err != nil || saved.At == nil {
			continue
		}

		if now-*saved.At < float64(uploadStateTTL.Milliseconds()) {
			state[key] = saved
		}
	}

	return state
}

// rememberUpload saves where an upload stands, or forgets it with nil. Uploads
// running at once take turns, so each save reads the one before it and none is lost.
func (c *Client) rememberUpload(key string, upload *savedUpload) error {
	c.stateMu.Lock()
	defer c.stateMu.Unlock()

	state := c.uploadState()

	if upload != nil {
		at := float64(c.now().UnixMilli())
		saved := *upload
		saved.At = &at
		state[key] = saved
	} else {
		delete(state, key)
	}

	if err := os.MkdirAll(filepath.Dir(c.stateFile), 0o777); err != nil {
		return err
	}

	var content bytes.Buffer
	encoder := json.NewEncoder(&content)
	encoder.SetEscapeHTML(false)

	if err := encoder.Encode(state); err != nil {
		return err
	}

	temporary := c.stateFile + "." + strconv.Itoa(os.Getpid()) + "." + rand.Text() + ".tmp"

	if err := os.WriteFile(temporary, bytes.TrimSuffix(content.Bytes(), []byte("\n")), 0o600); err != nil {
		return err
	}

	return rename(temporary, c.stateFile)
}

// offsetOf is how much of an upload the server holds; exists is false when
// the upload is gone.
func (c *Client) offsetOf(ctx context.Context, location string) (offset int64, exists bool, err error) {
	response, err := c.send(ctx, location, request{method: http.MethodHead})

	if err != nil {
		var refused *FileAPIError

		if errors.As(err, &refused) && (refused.Status == http.StatusNotFound || refused.Status == http.StatusGone) {
			return 0, false, nil
		}

		return 0, false, err
	}

	discard(response)
	offset, _ = strconv.ParseInt(response.Header.Get("Upload-Offset"), 10, 64)

	return offset, true, nil
}

// Upload sends one local file to remote, keeping its modification time.
// ModeCreate refuses to replace anything; ModeOverwrite replaces a file.
func (c *Client) Upload(ctx context.Context, local, remote string, options UploadOptions) error {
	if options.IfMatch != "" && options.Mode != ModeOverwrite {
		return errs.New("A version guard only guards an overwrite.")
	}

	info, err := os.Stat(local)

	if err != nil {
		return err
	}

	size := info.Size()
	var progressMu sync.Mutex

	progress := func(sent int64) {
		if options.OnProgress == nil {
			return
		}

		progressMu.Lock()
		defer progressMu.Unlock()
		options.OnProgress(sent)
	}

	sizeText := strconv.FormatInt(size, 10)
	key := strings.Join([]string{c.volumeID, remote, sizeText, FormatMilliseconds(Milliseconds(info.ModTime())), string(options.Mode), options.IfMatch}, "\n")
	modified := statTime(info.ModTime())

	pairs := []metadataPair{{"volume", c.volumeID}, {"path", remote}, {"mode", string(options.Mode)}}

	if options.IfMatch != "" {
		pairs = append(pairs, metadataPair{"ifMatch", `"` + options.IfMatch + `"`})
	}

	pairs = append(pairs, metadataPair{"parents", strconv.FormatBool(options.Parents)}, metadataPair{"modifiedAt", ISOTime(modified)})
	metadata := encodeMetadata(pairs...)
	location := ""
	offset := int64(0)
	exists := false

	if size <= inlineUploadBytes {
		unfinished, err := c.uploadInline(ctx, local, remote, size, modified, metadata)

		if err != nil {
			return err
		}

		if unfinished == nil {
			progress(size)
			return nil
		}

		location = unfinished.location
		offset = unfinished.offset
		exists = true
	} else {
		if size > min(c.chunkBytes, lanePieceBytes) {
			isSent, err := c.uploadLanes(ctx, local, size, key, metadata, progress)

			if err != nil {
				return err
			}

			if isSent {
				return c.rememberUpload(key, nil)
			}
		}

		location = c.uploadState()[key].URL

		if location != "" {
			offset, exists, err = c.offsetOf(ctx, location)

			if err != nil {
				return err
			}
		}
	}

	if !exists {
		header := map[string]string{"Upload-Length": sizeText, "Upload-Metadata": metadata}
		created, err := c.send(ctx, "/v1/uploads", request{method: http.MethodPost, header: header})

		if err != nil {
			return err
		}

		discard(created)
		location = created.Header.Get("Location")

		if location == "" {
			return errs.New("The file service did not return an upload location.")
		}

		offset = 0

		if err := c.rememberUpload(key, &savedUpload{URL: location}); err != nil {
			return err
		}
	}

	whole := part{location: location, local: local, base: 0, length: size, offset: offset, pieceBytes: c.chunkBytes, key: key}

	if err := c.sendFrom(ctx, whole, progress, nil); err != nil {
		return err
	}

	return c.rememberUpload(key, nil)
}

// uploadLanes sends the file as lanes at once: partial uploads of its parts
// that the server joins in place. False when the server doesn't confirm lanes;
// servers before them ignore Upload-Concat and would make a lane a whole upload.
func (c *Client) uploadLanes(ctx context.Context, local string, size int64, key, metadata string, progress func(int64)) (bool, error) {
	pieceBytes := min(c.chunkBytes, lanePieceBytes)
	count := min(int64(laneCount), (size+pieceBytes-1)/pieceBytes)
	laneBytes := (size + count - 1) / count
	bounds := make([][2]int64, count)

	for index := range bounds {
		start := int64(index) * laneBytes
		bounds[index] = [2]int64{start, min(start+laneBytes, size)}
	}

	sent := make([]int64, count)

	header := func(index int, group string) map[string]string {
		pairs := []metadataPair{{"offset", strconv.FormatInt(bounds[index][0], 10)}}

		if group != "" {
			pairs = append(pairs, metadataPair{"group", group})
		}

		return map[string]string{
			"Upload-Concat":   "partial",
			"Upload-Length":   strconv.FormatInt(bounds[index][1]-bounds[index][0], 10),
			"Upload-Metadata": metadata + "," + encodeMetadata(pairs...),
		}
	}

	lanes := c.uploadState()[key].Lanes

	if int64(len(lanes)) == count {
		held, err := c.laneOffsets(ctx, lanes)

		if err != nil {
			return false, err
		}

		if held == nil {
			lanes = nil
		} else {
			copy(sent, held)
		}
	} else {
		lanes = nil
	}

	if lanes == nil {
		created, err := c.createLanes(ctx, count, header)

		if err != nil || created == nil {
			return false, err
		}

		lanes = created

		if err := c.rememberUpload(key, &savedUpload{Lanes: lanes}); err != nil {
			return false, err
		}
	}

	var reportMu sync.Mutex

	report := func(index int) func(int64) {
		return func(laneSent int64) {
			reportMu.Lock()
			defer reportMu.Unlock()
			sent[index] = laneSent
			total := int64(0)

			for _, value := range sent {
				total += value
			}

			progress(total)
		}
	}

	// One lane failing stops the others, so none keeps sending after the upload gave up.
	halt, stop := context.WithCancel(ctx)
	defer stop()
	var group sync.WaitGroup
	var failureMu sync.Mutex
	var failure error

	for index, lane := range lanes {
		lanePart := part{
			location: lane, local: local, base: bounds[index][0], length: bounds[index][1] - bounds[index][0], offset: sent[index], pieceBytes: pieceBytes, key: key,
		}

		group.Go(func() {
			err := c.sendFrom(halt, lanePart, report(index), halt)

			if err == nil {
				return
			}

			failureMu.Lock()

			if failure == nil {
				failure = err
			}

			failureMu.Unlock()
			stop()
		})
	}

	group.Wait()

	if failure != nil {
		return false, failure
	}

	finalHeader := map[string]string{"Upload-Concat": "final;" + strings.Join(lanes, " "), "Upload-Metadata": metadata}
	final, err := c.send(ctx, "/v1/uploads", request{method: http.MethodPost, header: finalHeader})

	if err != nil {
		var refused *FileAPIError

		if errors.As(err, &refused) && !retryableStatuses[refused.Status] {
			if forgetErr := c.rememberUpload(key, nil); forgetErr != nil {
				return false, forgetErr
			}
		}

		return false, err
	}

	discard(final)

	return true, nil
}

// laneOffsets asks how much of each lane the server holds; nil when one is gone.
func (c *Client) laneOffsets(ctx context.Context, lanes []string) ([]int64, error) {
	held := make([]int64, len(lanes))
	present := make([]bool, len(lanes))
	failures := make([]error, len(lanes))
	var group sync.WaitGroup

	for index, lane := range lanes {
		group.Go(func() {
			held[index], present[index], failures[index] = c.offsetOf(ctx, lane)
		})
	}

	group.Wait()

	for _, err := range failures {
		if err != nil {
			return nil, err
		}
	}

	for _, isPresent := range present {
		if !isPresent {
			return nil, nil
		}
	}

	return held, nil
}

// createLanes starts the lane uploads; nil when the server does not join lanes.
func (c *Client) createLanes(ctx context.Context, count int64, header func(index int, group string) map[string]string) ([]string, error) {
	first, err := c.once(ctx, "/v1/uploads", request{method: http.MethodPost, header: header(0, "")}, false)

	if err != nil {
		return nil, ctx.Err()
	}

	discard(first)
	location := first.Header.Get("Location")
	isCreated := first.StatusCode == http.StatusCreated && location != ""

	if !isCreated || first.Header.Get("Upload-Concat") != "partial" {
		if isCreated {
			dropped, err := c.once(ctx, location, request{method: http.MethodDelete}, false)

			if err == nil {
				discard(dropped)
			}
		}

		return nil, nil
	}

	group := location[strings.LastIndex(location, "/")+1:]
	locations := make([]string, count)
	locations[0] = location
	failures := make([]error, count)
	var wait sync.WaitGroup

	for index := 1; index < int(count); index++ {
		wait.Go(func() {
			created, err := c.send(ctx, "/v1/uploads", request{method: http.MethodPost, header: header(index, group)})

			if err != nil {
				failures[index] = err
				return
			}

			discard(created)
			locations[index] = created.Header.Get("Location")
		})
	}

	wait.Wait()

	for _, failure := range failures {
		if failure != nil {
			return nil, failure
		}
	}

	return locations, nil
}

// part is bytes [base+offset, base+length) of local, sent to the upload at location.
type part struct {
	location   string
	local      string
	base       int64
	length     int64
	offset     int64
	pieceBytes int64
	key        string
}

var errUploadStopped = errs.New("The upload stopped.")

// sendFrom sends a part in pieces, asking the server how much it kept after a
// dropped connection or a retryable answer. onSent counts bytes of this part;
// halt, when given, stops it.
func (c *Client) sendFrom(ctx context.Context, upload part, onSent func(int64), halt context.Context) error {
	file, err := os.Open(upload.local)

	if err != nil {
		return err
	}

	defer file.Close()
	isHalted := func() bool { return halt != nil && halt.Err() != nil }
	offset := upload.offset

	for attempt := 0; offset < upload.length; {
		if isHalted() {
			return errUploadStopped
		}

		start := offset
		response, _ := c.sendPiece(ctx, file, upload, start, onSent)

		if isHalted() {
			discard(response)
			return errUploadStopped
		}

		if response != nil && response.StatusCode == http.StatusNoContent {
			discard(response)
			held, err := strconv.ParseInt(response.Header.Get("Upload-Offset"), 10, 64)

			if err != nil {
				held = start
			}

			offset = held
			attempt = 0

			continue
		}

		// 409: the server holds a different amount than was sent.
		if response != nil && !retryableStatuses[response.StatusCode] && response.StatusCode != http.StatusConflict {
			refused := refusal(response)

			if refused.Status == http.StatusNotFound || refused.Status == http.StatusGone {
				if err := c.rememberUpload(upload.key, nil); err != nil {
					return err
				}
			}

			return refused
		}

		if attempt >= len(retryDelays) {
			if response != nil {
				return refusal(response)
			}

			return errs.New("The upload kept losing its connection. Run the command again to resume.")
		}

		wait := c.delay(attempt, retryAfter(response))
		discard(response)

		if err := sleep(ctx, wait); err != nil {
			return err
		}

		attempt++
		held, exists, err := c.offsetOf(ctx, upload.location)

		if isHalted() {
			return errUploadStopped
		}

		if err != nil {
			return err
		}

		if !exists {
			if err := c.rememberUpload(upload.key, nil); err != nil {
				return err
			}

			return errs.New("The upload expired on the server. Run the command again to start over.")
		}

		offset = held
		onSent(offset)
	}

	return nil
}

// sendPiece PATCHes one piece from start, given up when its bytes stop moving
// for the stall time; nil means the connection was lost.
func (c *Client) sendPiece(ctx context.Context, file *os.File, upload part, start int64, onSent func(int64)) (*http.Response, error) {
	stalled, giveUp := context.WithCancel(ctx)
	defer giveUp()
	var moved atomic.Int64
	moved.Store(c.now().UnixNano())
	var isDone atomic.Bool
	defer isDone.Store(true)
	watchdog := time.NewTicker(min(c.stall, 5*time.Second))
	defer watchdog.Stop()

	go func() {
		for {
			select {
			case <-stalled.Done():
				return
			case <-watchdog.C:
				if c.now().UnixNano()-moved.Load() > int64(c.stall) {
					giveUp()
					return
				}
			}
		}
	}()

	end := min(start+upload.pieceBytes, upload.length)

	body := func() (io.Reader, int64, error) {
		section := io.NewSectionReader(file, upload.base+start, end-start)

		counted := &countingReader{reader: section, onRead: func(count int64) {
			// The transport may still read after giving up; that is no progress.
			if isDone.Load() {
				return
			}

			moved.Store(c.now().UnixNano())
			onSent(start + count)
		}}

		return counted, end - start, nil
	}

	header := map[string]string{"Upload-Offset": strconv.FormatInt(start, 10), "Content-Type": "application/offset+octet-stream"}
	response, err := c.once(stalled, upload.location, request{method: http.MethodPatch, header: header, body: body}, false)

	if err != nil {
		return nil, err
	}

	// The answer is read before the request's context ends with this call.
	content, err := io.ReadAll(response.Body)
	response.Body.Close()

	if err != nil || stalled.Err() != nil {
		return nil, errors.Join(err, stalled.Err())
	}

	response.Body = io.NopCloser(bytes.NewReader(content))

	return response, nil
}

type unfinishedUpload struct {
	location string
	offset   int64
}

// uploadInline sends a small file in the creating request; a lost answer is
// checked against what landed. It returns the upload when the server kept only part.
func (c *Client) uploadInline(ctx context.Context, local, remote string, size int64, modified time.Time, metadata string) (*unfinishedUpload, error) {
	content, err := os.ReadFile(local)

	if err != nil {
		return nil, err
	}

	header := map[string]string{
		"Upload-Length":   strconv.FormatInt(size, 10),
		"Upload-Metadata": metadata,
		"Content-Type":    "application/offset+octet-stream",
	}

	for attempt := 0; ; attempt++ {
		response, err := c.once(ctx, "/v1/uploads", request{method: http.MethodPost, header: header, body: bytesBody(content)}, false)

		if err != nil {
			response = nil

			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
		}

		if response != nil && response.StatusCode == http.StatusCreated {
			discard(response)
			offset, _ := strconv.ParseInt(response.Header.Get("Upload-Offset"), 10, 64)
			location := response.Header.Get("Location")

			if offset == size {
				return nil, nil
			}

			if location != "" {
				return &unfinishedUpload{location: location, offset: offset}, nil
			}
		}

		if response != nil && !retryableStatuses[response.StatusCode] && response.StatusCode != http.StatusCreated {
			refused := refusal(response)

			// An earlier attempt whose answer was lost may have placed this very file.
			if attempt > 0 && refused.Code == "already_exists" {
				landed, err := c.Entry(ctx, remote)

				if err != nil {
					return nil, err
				}

				if landed != nil && landed.Type == TypeFile && landed.SizeBytes != nil && *landed.SizeBytes == size {
					landedAt, ok := ParseTime(landed.ModifiedAt)

					if ok && landedAt == modified.UnixMilli() {
						return nil, nil
					}
				}
			}

			return nil, refused
		}

		if attempt >= len(retryDelays) {
			if response != nil {
				return nil, refusal(response)
			}

			return nil, errs.New("The upload kept losing its connection. Run the command again.")
		}

		discard(response)

		if err := sleep(ctx, c.delay(attempt, nil)); err != nil {
			return nil, err
		}
	}
}
