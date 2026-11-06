// Package filestest is just enough of the file API for tests: uploads, ranged
// downloads, listings, find streams and copies, with faults on request.
package filestest

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"featherless.ai/featherless-cloud/internal/api"
)

const (
	Volume = "11111111-2222-4333-8444-555555555555"
	// Backup is another volume of the organization, for copies between volumes.
	Backup = "22222222-2222-4333-8444-555555555555"
)

const folderModifiedAt = "2026-01-01T00:00:00.000Z"

type StoredFile struct {
	Data       []byte
	ModifiedAt string
	Version    string
}

func Stored(text, modifiedAt, version string) *StoredFile {
	return &StoredFile{Data: []byte(text), ModifiedAt: modifiedAt, Version: version}
}

type Upload struct {
	Path       string
	Size       int64
	Data       []byte
	Received   int64
	ModifiedAt string
	Partial    bool
}

// Server is the fake. Tests set and read its fields between requests; the
// handler takes mu, as lanes reach it at once.
type Server struct {
	Files  map[string]*StoredFile
	Backup map[string]*StoredFile
	// FailNextCopyLate makes the next copy or move answer 200 at once, then fail with this code.
	FailNextCopyLate string
	// FindEnd is how the next find ends after its matches: "" (done), "end"
	// (a clean end without done), "reset" (a cut connection) or "error" (an error line).
	FindEnd string
	// FindPageAt, when set, makes one find page hold this many matches, then end with a cursor.
	FindPageAt int
	// FindStall makes find pages stop "no-cursor", or repeat the "same-cursor", so the client gives up.
	FindStall string
	// Dirs are folders made by mkdir, as the file server holds them.
	Dirs map[string]bool
	// LastMetadata is the Upload-Metadata of the last upload created.
	LastMetadata map[string]string
	// ListVersions are versions listings report instead of the stored one, as a listing read before a change.
	ListVersions        map[string]string
	ListDelay           time.Duration
	MaxListingsInFlight int
	FindQuery           url.Values
	Uploads             map[string]*Upload
	// Lanes joins lanes, as file servers since parallel uploads; older ones ignore Upload-Concat.
	Lanes        bool
	LanesCreated int
	Joins        int
	// FailPatches is how many PATCHes still answer 500.
	FailPatches int
	// HangNextPatch takes the next PATCH in whole and never answers, as over a connection that stalls.
	HangNextPatch bool
	// CutNextPatchAfter, when set, keeps that many bytes of the next PATCH and drops the connection.
	CutNextPatchAfter int64
	// CutNextDownloadAfter, when set, sends that many bytes of the next download and drops the connection.
	CutNextDownloadAfter int64
	// MaxPatchBytes, when set, is like Cloudflare's request body limit.
	MaxPatchBytes int64
	// Starting is how many requests, by method, still answer as a file server that is starting.
	Starting map[string]int
	// StoppedSinceSession, when set, answers files_starting until a session newer than this one,
	// as when a start takes too long.
	StoppedSinceSession int
	ExpireNextToken     bool
	Sessions            int
	Requests            []string
	URL                 string

	mu               sync.Mutex
	listingsInFlight int
	closing          chan struct{}
}

// Start serves a fresh fake until the test ends.
func Start(t testing.TB) *Server {
	t.Helper()

	fake := &Server{
		Files:        map[string]*StoredFile{},
		Backup:       map[string]*StoredFile{},
		Dirs:         map[string]bool{},
		ListVersions: map[string]string{},
		Uploads:      map[string]*Upload{},
		Starting:     map[string]int{},
		closing:      make(chan struct{}),
	}

	server := httptest.NewServer(fake)
	fake.URL = server.URL

	t.Cleanup(func() {
		close(fake.closing)
		server.CloseClientConnections()
		server.Close()
	})

	return fake
}

// CreateFileSession issues a token for ten minutes, counting sessions.
func (s *Server) CreateFileSession(ctx context.Context, volumeID string) (api.FileSession, error) {
	s.mu.Lock()
	s.Sessions++
	token := fmt.Sprintf("token-%d", s.Sessions)
	s.mu.Unlock()

	expiresAt := time.Now().Add(10 * time.Minute).UTC().Format("2006-01-02T15:04:05.000Z")

	return api.FileSession{Token: token, ExpiresAt: expiresAt, BaseURL: s.URL}, nil
}

type entryJSON struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	Type       string `json:"type"`
	SizeBytes  *int   `json:"sizeBytes,omitempty"`
	ModifiedAt string `json:"modifiedAt"`
	Version    string `json:"version,omitempty"`
}

func fileJSON(name, path string, file *StoredFile, version string) entryJSON {
	size := len(file.Data)
	return entryJSON{Name: name, Path: path, Type: "file", SizeBytes: &size, ModifiedAt: file.ModifiedAt, Version: version}
}

func folderJSON(name, path string) entryJSON {
	return entryJSON{Name: name, Path: path, Type: "directory", ModifiedAt: folderModifiedAt}
}

func encode(value any) string {
	text, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}

	return string(text)
}

func refusalJSON(code, message string) string {
	return encode(map[string]map[string]string{"error": {"code": code, "message": message}})
}

func respond(w http.ResponseWriter, status int, headers map[string]string, body string) {
	for name, value := range headers {
		w.Header().Set(name, value)
	}

	w.WriteHeader(status)
	io.WriteString(w, body)
}

func respondJSON(w http.ResponseWriter, status int, body string) {
	respond(w, status, map[string]string{"Content-Type": "application/json"}, body)
}

// cut drops the connection; what was flushed before stays sent.
func cut() {
	panic(http.ErrAbortHandler)
}

type findEnd struct {
	Done   bool   `json:"done"`
	Cursor string `json:"cursor,omitempty"`
}

func lastPart(path string) string {
	return path[strings.LastIndex(path, "/")+1:]
}

func (s *Server) volume(id string) map[string]*StoredFile {
	if id == Backup {
		return s.Backup
	}

	return s.Files
}

// finish stores a whole upload as a file; callers hold mu.
func (s *Server) finish(upload *Upload) {
	if upload.Received == upload.Size && !upload.Partial {
		version := fmt.Sprintf("v%d%d", len(s.Files), time.Now().UnixMilli())
		s.Files[upload.Path] = &StoredFile{Data: bytes.Clone(upload.Data), ModifiedAt: upload.ModifiedAt, Version: version}
	}
}

// body reads up to limit bytes (0: all); cut reports that it stopped there.
func body(r *http.Request, limit int64) (data []byte, isCut bool) {
	if limit <= 0 {
		data, _ = io.ReadAll(r.Body)
		return data, false
	}

	data, _ = io.ReadAll(io.LimitReader(r.Body, limit))
	return data, int64(len(data)) >= limit
}

// wait sleeps, or returns early when the test ends.
func (s *Server) wait(r *http.Request, delay time.Duration) {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-timer.C:
	case <-r.Context().Done():
	case <-s.closing:
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.Requests = append(s.Requests, r.Method+" "+r.URL.Path)
	isStopped := s.StoppedSinceSession > 0 && s.Sessions <= s.StoppedSinceSession
	isStarting := !isStopped && s.Starting[r.Method] > 0

	if isStarting {
		s.Starting[r.Method]--
	}

	isExpired := !isStopped && !isStarting && s.ExpireNextToken

	if isExpired {
		s.ExpireNextToken = false
	}

	s.mu.Unlock()

	if isStopped || isStarting {
		headers := map[string]string{"Retry-After": "5", "Content-Type": "application/json"}
		respond(w, http.StatusServiceUnavailable, headers, refusalJSON("files_starting", "starting"))
		return
	}

	if isExpired {
		respondJSON(w, http.StatusUnauthorized, refusalJSON("token_expired", "expired"))
		return
	}

	path := r.URL.Path

	if r.Method == http.MethodPost && path == "/v1/uploads" {
		s.createUpload(w, r)
		return
	}

	s.mu.Lock()
	upload := s.Uploads[strings.TrimPrefix(path, "/v1/uploads/")]
	s.mu.Unlock()

	if upload != nil {
		switch r.Method {
		case http.MethodHead:
			s.mu.Lock()
			offset := strconv.FormatInt(upload.Received, 10)
			s.mu.Unlock()
			respond(w, http.StatusOK, map[string]string{"Upload-Offset": offset}, "")
			return
		case http.MethodDelete:
			s.mu.Lock()
			delete(s.Uploads, strings.TrimPrefix(path, "/v1/uploads/"))
			s.mu.Unlock()
			w.WriteHeader(http.StatusNoContent)
			return
		case http.MethodPatch:
			s.patch(w, r, upload)
			return
		}
	}

	parts := strings.Split(path, "/")
	volumeID := ""

	if len(parts) > 3 {
		volumeID = parts[3]
	}

	switch {
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/directories"):
		s.mkdir(w, r, volumeID)
	case r.Method == http.MethodPost && (strings.HasSuffix(path, "/copy") || strings.HasSuffix(path, "/move")):
		s.copy(w, r, volumeID)
	case strings.HasSuffix(path, "/entry"):
		s.entry(w, r, volumeID)
	case strings.HasSuffix(path, "/content"):
		s.content(w, r, volumeID)
	case strings.HasSuffix(path, "/find"):
		s.find(w, r, volumeID)
	case strings.HasSuffix(path, "/files"):
		s.list(w, r, volumeID)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func parseMetadata(header string) map[string]string {
	metadata := map[string]string{}

	for pair := range strings.SplitSeq(header, ",") {
		key, value, _ := strings.Cut(pair, " ")
		decoded, _ := base64.StdEncoding.DecodeString(value)
		metadata[key] = string(decoded)
	}

	return metadata
}

func (s *Server) createUpload(w http.ResponseWriter, r *http.Request) {
	metadata := parseMetadata(r.Header.Get("Upload-Metadata"))

	s.mu.Lock()
	s.LastMetadata = metadata
	existing := s.Files[metadata["path"]]

	if metadata["mode"] == "create" && existing != nil {
		s.mu.Unlock()
		respondJSON(w, http.StatusPreconditionFailed, refusalJSON("already_exists", "exists"))
		return
	}

	isStale := metadata["ifMatch"] != "" && (existing == nil || `"`+existing.Version+`"` != metadata["ifMatch"])

	if isStale {
		s.mu.Unlock()
		respondJSON(w, http.StatusPreconditionFailed, refusalJSON("precondition_failed", "changed"))
		return
	}

	concat := ""

	if s.Lanes {
		concat = r.Header.Get("Upload-Concat")
	}

	id := fmt.Sprintf("%s.%d", Volume, len(s.Uploads))
	location := s.URL + "/v1/uploads/" + id

	if lanes, isFinal := strings.CutPrefix(concat, "final;"); isFinal {
		var joined []byte

		for lane := range strings.SplitSeq(strings.TrimSpace(lanes), " ") {
			partial := s.Uploads[lastPart(lane)]

			if partial == nil || !partial.Partial || partial.Received != partial.Size {
				s.mu.Unlock()
				w.WriteHeader(http.StatusBadRequest)
				return
			}

			joined = append(joined, partial.Data...)
		}

		version := fmt.Sprintf("v%d%d", len(s.Files), time.Now().UnixMilli())
		s.Files[metadata["path"]] = &StoredFile{Data: joined, ModifiedAt: metadata["modifiedAt"], Version: version}
		s.Joins++
		s.Uploads[id] = &Upload{Path: metadata["path"], ModifiedAt: metadata["modifiedAt"]}
		s.mu.Unlock()

		respond(w, http.StatusCreated, map[string]string{"Location": location}, "")
		return
	}

	size, _ := strconv.ParseInt(r.Header.Get("Upload-Length"), 10, 64)
	upload := &Upload{Path: metadata["path"], Size: size, ModifiedAt: metadata["modifiedAt"], Partial: concat == "partial"}
	s.Uploads[id] = upload

	if upload.Partial {
		s.LanesCreated++
		w.Header().Set("Upload-Concat", "partial")
	}

	s.mu.Unlock()

	if r.Header.Get("Content-Type") == "application/offset+octet-stream" {
		data, _ := body(r, 0)
		s.mu.Lock()
		upload.Data = append(upload.Data, data...)
		upload.Received += int64(len(data))
		s.finish(upload)
		s.mu.Unlock()
	}

	s.mu.Lock()
	offset := strconv.FormatInt(upload.Received, 10)
	s.mu.Unlock()

	respond(w, http.StatusCreated, map[string]string{"Location": location, "Upload-Offset": offset}, "")
}

func (s *Server) patch(w http.ResponseWriter, r *http.Request, upload *Upload) {
	s.mu.Lock()

	if s.HangNextPatch {
		s.HangNextPatch = false
		s.mu.Unlock()

		data, _ := body(r, 0)
		s.mu.Lock()
		upload.Data = append(upload.Data, data...)
		upload.Received += int64(len(data))
		s.finish(upload)
		s.mu.Unlock()

		select {
		case <-r.Context().Done():
		case <-s.closing:
		}

		cut()
	}

	if s.FailPatches > 0 {
		s.FailPatches--
		s.mu.Unlock()
		body(r, 0)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	offset, _ := strconv.ParseInt(r.Header.Get("Upload-Offset"), 10, 64)

	if offset != upload.Received {
		s.mu.Unlock()
		w.WriteHeader(http.StatusConflict)
		return
	}

	limit := s.CutNextPatchAfter
	s.CutNextPatchAfter = 0
	maxBytes := s.MaxPatchBytes
	s.mu.Unlock()

	data, isCut := body(r, limit)

	if maxBytes > 0 && int64(len(data)) > maxBytes {
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		return
	}

	s.mu.Lock()
	upload.Data = append(upload.Data, data...)
	upload.Received += int64(len(data))

	if isCut {
		s.mu.Unlock()
		cut()
	}

	s.finish(upload)
	received := strconv.FormatInt(upload.Received, 10)
	s.mu.Unlock()

	respond(w, http.StatusNoContent, map[string]string{"Upload-Offset": received}, "")
}

func (s *Server) mkdir(w http.ResponseWriter, r *http.Request, volumeID string) {
	data, _ := body(r, 0)
	var request struct {
		Path    string `json:"path"`
		Parents bool   `json:"parents"`
	}

	json.Unmarshal(data, &request)

	s.mu.Lock()

	if s.volume(volumeID)[request.Path] != nil {
		s.mu.Unlock()
		respondJSON(w, http.StatusConflict, refusalJSON("not_a_directory", "A file is in the way."))
		return
	}

	parts := strings.Split(request.Path, "/")
	depth := len(parts)

	if request.Parents {
		depth = 1
	}

	for ; depth <= len(parts); depth++ {
		s.Dirs[strings.Join(parts[:depth], "/")] = true
	}

	s.mu.Unlock()

	respondJSON(w, http.StatusCreated, encode(folderJSON(parts[len(parts)-1], request.Path)))
}

func (s *Server) copy(w http.ResponseWriter, r *http.Request, volumeID string) {
	data, _ := body(r, 0)
	var request struct {
		FromVolume string `json:"fromVolume"`
		From       string `json:"from"`
		To         string `json:"to"`
		Overwrite  bool   `json:"overwrite"`
		Parents    bool   `json:"parents"`
	}

	json.Unmarshal(data, &request)

	s.mu.Lock()
	source := s.volume(request.FromVolume)
	target := s.volume(volumeID)
	file := source[request.From]

	if file == nil {
		s.mu.Unlock()
		respondJSON(w, http.StatusNotFound, refusalJSON("not_found", "not found"))
		return
	}

	if !request.Overwrite && target[request.To] != nil {
		s.mu.Unlock()
		respondJSON(w, http.StatusPreconditionFailed, refusalJSON("already_exists", "exists"))
		return
	}

	failure := s.FailNextCopyLate
	s.FailNextCopyLate = ""

	if failure == "" {
		copied := *file
		target[request.To] = &copied

		if strings.HasSuffix(r.URL.Path, "/move") {
			delete(source, request.From)
		}
	}

	s.mu.Unlock()

	// A long copy: the status and spaces go out before the result.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	io.WriteString(w, "  ")
	w.(http.Flusher).Flush()

	if failure != "" {
		io.WriteString(w, refusalJSON(failure, "The volume is full."))
		return
	}

	io.WriteString(w, encode(fileJSON(lastPart(request.To), request.To, file, file.Version)))
}

func (s *Server) entry(w http.ResponseWriter, r *http.Request, volumeID string) {
	path := r.URL.Query().Get("path")

	s.mu.Lock()
	files := s.volume(volumeID)
	file := files[path]

	if r.Method == http.MethodDelete {
		delete(files, path)
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
		return
	}

	isFolder := s.Dirs[path]

	for key := range files {
		if strings.HasPrefix(key, path+"/") {
			isFolder = true
		}
	}

	version := ""

	if file != nil {
		version = file.Version

		if listed, ok := s.ListVersions[path]; ok {
			version = listed
		}
	}

	s.mu.Unlock()

	if file == nil && !isFolder {
		respondJSON(w, http.StatusNotFound, refusalJSON("not_found", "not found"))
		return
	}

	if file != nil {
		respondJSON(w, http.StatusOK, encode(fileJSON(lastPart(path), path, file, version)))
		return
	}

	respondJSON(w, http.StatusOK, encode(folderJSON(lastPart(path), path)))
}

var rangePattern = regexp.MustCompile(`bytes=(\d+)-`)

func (s *Server) content(w http.ResponseWriter, r *http.Request, volumeID string) {
	path := r.URL.Query().Get("path")

	s.mu.Lock()
	file := s.volume(volumeID)[path]
	limit := s.CutNextDownloadAfter
	s.CutNextDownloadAfter = 0
	s.mu.Unlock()

	if file == nil {
		respondJSON(w, http.StatusNotFound, refusalJSON("not_found", "not found"))
		return
	}

	match := rangePattern.FindStringSubmatch(r.Header.Get("Range"))
	isRanged := match != nil && r.Header.Get("If-Range") == `"`+file.Version+`"`
	start := 0
	status := http.StatusOK

	if isRanged {
		start, _ = strconv.Atoi(match[1])
		status = http.StatusPartialContent
	}

	content := file.Data[min(start, len(file.Data)):]
	headers := map[string]string{"Content-Length": strconv.Itoa(len(content)), "ETag": `"` + file.Version + `"`}

	for name, value := range headers {
		w.Header().Set(name, value)
	}

	w.WriteHeader(status)

	if limit > 0 {
		w.Write(content[:min(int(limit), len(content))])
		w.(http.Flusher).Flush()
		cut()
	}

	w.Write(content)
}

func (s *Server) find(w http.ResponseWriter, r *http.Request, volumeID string) {
	query := r.URL.Query()
	path := query.Get("path")
	cursor := query.Get("cursor")

	s.mu.Lock()
	s.FindQuery = query
	end := s.FindEnd
	s.FindEnd = ""
	pageAt := s.FindPageAt
	stall := s.FindStall
	files := s.volume(volumeID)
	var names []string

	for name := range files {
		if path == "" || strings.HasPrefix(name, path+"/") {
			names = append(names, name)
		}
	}

	slices.Sort(names)
	after := ""

	if cursor != "" {
		decoded, _ := base64.RawURLEncoding.DecodeString(cursor)
		var position struct {
			A string `json:"a"`
		}

		json.Unmarshal(decoded, &position)
		after = position.A
	}

	var remaining []string

	for _, name := range names {
		if name > after {
			remaining = append(remaining, name)
		}
	}

	page := remaining

	if pageAt > 0 {
		page = remaining[:min(pageAt, len(remaining))]
	}

	var lines strings.Builder
	lines.WriteString("\n")

	for _, name := range page {
		lines.WriteString(encode(fileJSON(lastPart(name), name, files[name], files[name].Version)) + "\n")
	}

	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.WriteHeader(http.StatusOK)
	// An empty line first, as the file server keeps a long walk open.
	io.WriteString(w, lines.String())
	w.(http.Flusher).Flush()

	if end == "reset" {
		cut()
	}

	closings := map[string]string{
		"":      `{"done":true}` + "\n",
		"end":   "",
		"error": refusalJSON("unavailable", "File access is temporarily unavailable. Retry shortly.") + "\n",
	}

	closing := closings[end]

	if pageAt == 0 || end != "" {
		io.WriteString(w, closing)
		return
	}

	if stall == "no-cursor" {
		io.WriteString(w, encode(findEnd{Done: false})+"\n")
		return
	}

	if stall == "same-cursor" && after != "" {
		io.WriteString(w, encode(findEnd{Done: false, Cursor: cursor})+"\n")
		return
	}

	if len(remaining) > len(page) {
		next := base64.RawURLEncoding.EncodeToString([]byte(encode(map[string]string{"a": page[len(page)-1]})))
		io.WriteString(w, encode(findEnd{Done: false, Cursor: next})+"\n")
		return
	}

	io.WriteString(w, closing)
}

func (s *Server) list(w http.ResponseWriter, r *http.Request, volumeID string) {
	path := r.URL.Query().Get("path")
	cursor := r.URL.Query().Get("cursor")

	// A listing takes ListDelay, as over a network, so listings sent at once overlap.
	s.mu.Lock()
	s.listingsInFlight++
	s.MaxListingsInFlight = max(s.MaxListingsInFlight, s.listingsInFlight)
	delay := s.ListDelay
	s.mu.Unlock()

	s.wait(r, delay)

	s.mu.Lock()
	s.listingsInFlight--
	files := s.volume(volumeID)
	folders := map[string]bool{}

	for folder := range s.Dirs {
		folders[folder] = true
	}

	for name := range files {
		parts := strings.Split(name, "/")

		for depth := 1; depth < len(parts); depth++ {
			folders[strings.Join(parts[:depth], "/")] = true
		}
	}

	prefix := ""

	if path != "" {
		prefix = path + "/"
	}

	children := map[string]*StoredFile{}

	for folder := range folders {
		rest, isBelow := strings.CutPrefix(folder, prefix)

		if isBelow && !strings.Contains(rest, "/") {
			children[rest] = nil
		}
	}

	for name, file := range files {
		rest, isBelow := strings.CutPrefix(name, prefix)

		if isBelow && !strings.Contains(rest, "/") {
			children[rest] = file
		}
	}

	after := ""

	if cursor != "" {
		decoded, _ := base64.RawURLEncoding.DecodeString(cursor)
		after = string(decoded)
	}

	var later []string

	for name := range children {
		if name > after {
			later = append(later, name)
		}
	}

	slices.Sort(later)
	page := later[:min(2, len(later))]
	entries := []entryJSON{}

	for _, name := range page {
		file := children[name]

		if file == nil {
			entries = append(entries, folderJSON(name, prefix+name))
			continue
		}

		version := file.Version

		if listed, ok := s.ListVersions[prefix+name]; ok {
			version = listed
		}

		entries = append(entries, fileJSON(name, prefix+name, file, version))
	}

	s.mu.Unlock()

	listing := struct {
		Path       string      `json:"path"`
		Entries    []entryJSON `json:"entries"`
		NextCursor string      `json:"nextCursor,omitempty"`
	}{Path: "", Entries: entries}

	if len(later) > 2 {
		listing.NextCursor = base64.RawURLEncoding.EncodeToString([]byte(page[len(page)-1]))
	}

	respondJSON(w, http.StatusOK, encode(listing))
}
