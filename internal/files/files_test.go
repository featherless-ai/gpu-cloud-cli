package files

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"featherless.ai/featherless-cloud/internal/files/filestest"
)

type testEnv struct {
	ctx    context.Context
	fake   *filestest.Server
	dir    string
	client *Client
}

func setUp(t *testing.T) *testEnv {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)

	env := &testEnv{ctx: ctx, fake: filestest.Start(t), dir: t.TempDir()}
	env.client = env.newClient(Options{})

	return env
}

// newClient retries after a millisecond and keeps its upload state in the test folder.
func (e *testEnv) newClient(options Options) *Client {
	if options.Delay == nil {
		options.Delay = func(int, *time.Duration) time.Duration { return time.Millisecond }
	}

	if options.StateFile == "" {
		options.StateFile = filepath.Join(e.dir, "uploads.json")
	}

	return NewClient(e.fake, filestest.Volume, options)
}

func writeFile(t *testing.T, path string, content []byte) {
	t.Helper()

	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
}

// filled is size bytes of value ending in "end", so a lost tail shows.
func filled(size int, value byte) []byte {
	content := bytes.Repeat([]byte{value}, size)
	copy(content[size-3:], "end")

	return content
}

func countRequests(requests []string, match func(string) bool) int {
	count := 0

	for _, request := range requests {
		if match(request) {
			count++
		}
	}

	return count
}

func assertStored(t *testing.T, fake *filestest.Server, path string, want []byte) {
	t.Helper()
	file := fake.Files[path]

	if file == nil {
		t.Fatalf("%s was not stored", path)
	}

	if !bytes.Equal(file.Data, want) {
		t.Errorf("%s holds %d bytes that differ from the %d sent", path, len(file.Data), len(want))
	}
}

func assertText(t *testing.T, path, want string) {
	t.Helper()
	content, err := os.ReadFile(path)

	if err != nil {
		t.Fatal(err)
	}

	if string(content) != want {
		t.Errorf("%s = %q, want %q", path, content, want)
	}
}

func uploadMtimeMs(info os.FileInfo) string {
	modified := info.ModTime()
	milliseconds := float64(modified.Unix())*1000 + float64(modified.Nanosecond())/1e6

	return strconv.FormatFloat(milliseconds, 'f', -1, 64)
}

func TestResumesALargeUploadFromWhatTheServerKeptWhenTheConnectionDrops(t *testing.T) {
	env := setUp(t)
	local := filepath.Join(env.dir, "weights.bin")
	content := filled(6*1024*1024, 3)
	writeFile(t, local, content)
	env.fake.CutNextPatchAfter = 1_000_000
	var progress []int64

	options := UploadOptions{Mode: ModeCreate, Parents: true, OnProgress: func(sent int64) { progress = append(progress, sent) }}
	err := env.client.Upload(env.ctx, local, "models/weights.bin", options)

	if err != nil {
		t.Fatal(err)
	}

	assertStored(t, env.fake, "models/weights.bin", content)
	heads := countRequests(env.fake.Requests, func(request string) bool { return strings.HasPrefix(request, "HEAD") })

	if heads != 1 {
		t.Errorf("HEAD requests = %d, want 1", heads)
	}

	if len(progress) == 0 || progress[len(progress)-1] != int64(len(content)) {
		t.Errorf("last progress = %v, want %d", progress, len(content))
	}

	assertText(t, filepath.Join(env.dir, "uploads.json"), "{}")
}

func TestGivesUpOnARequestThatStopsMovingAndResumesFromWhatTheServerKept(t *testing.T) {
	env := setUp(t)
	local := filepath.Join(env.dir, "weights.bin")
	content := filled(6*1024*1024+7, 6)
	writeFile(t, local, content)
	env.fake.HangNextPatch = true
	client := env.newClient(Options{Stall: 200 * time.Millisecond})

	err := client.Upload(env.ctx, local, "weights.bin", UploadOptions{Mode: ModeCreate})

	if err != nil {
		t.Fatal(err)
	}

	assertStored(t, env.fake, "weights.bin", content)
	heads := countRequests(env.fake.Requests, func(request string) bool { return strings.HasPrefix(request, "HEAD") })

	if heads != 1 {
		t.Errorf("HEAD requests = %d, want 1", heads)
	}
}

func TestSendsALargeFileInPiecesUnderTheRequestBodyLimit(t *testing.T) {
	env := setUp(t)
	local := filepath.Join(env.dir, "weights.bin")
	content := filled(6*1024*1024+5, 4)
	writeFile(t, local, content)
	env.fake.MaxPatchBytes = 2 * 1024 * 1024
	client := env.newClient(Options{ChunkBytes: 2 * 1024 * 1024})

	err := client.Upload(env.ctx, local, "weights.bin", UploadOptions{Mode: ModeCreate})

	if err != nil {
		t.Fatal(err)
	}

	assertStored(t, env.fake, "weights.bin", content)
	patches := countRequests(env.fake.Requests, func(request string) bool { return strings.HasPrefix(request, "PATCH") })

	if patches != 4 {
		t.Errorf("PATCH requests = %d, want 4", patches)
	}
}

// laneContent holds its own offset every 4 KiB, so a lane joined out of order shows.
func laneContent() []byte {
	content := make([]byte, 7*1024*1024+5)

	for offset := 0; offset+4 <= len(content); offset += 4096 {
		binary.LittleEndian.PutUint32(content[offset:], uint32(offset))
	}

	return content
}

func setUpLanes(t *testing.T) (*testEnv, []byte) {
	env := setUp(t)
	env.client = env.newClient(Options{ChunkBytes: 2 * 1024 * 1024})

	return env, laneContent()
}

func TestLanesSendsALargeFileAsFourLanesAtOnceThatTheServerJoins(t *testing.T) {
	env, content := setUpLanes(t)
	env.fake.Lanes = true
	local := filepath.Join(env.dir, "weights.bin")
	writeFile(t, local, content)
	var mu sync.Mutex
	var progress []int64

	onProgress := func(sent int64) {
		mu.Lock()
		defer mu.Unlock()
		progress = append(progress, sent)
	}

	err := env.client.Upload(env.ctx, local, "weights.bin", UploadOptions{Mode: ModeCreate, OnProgress: onProgress})

	if err != nil {
		t.Fatal(err)
	}

	assertStored(t, env.fake, "weights.bin", content)

	if env.fake.LanesCreated != 4 || env.fake.Joins != 1 {
		t.Errorf("lanes created, joins = %d, %d, want 4, 1", env.fake.LanesCreated, env.fake.Joins)
	}

	if len(progress) == 0 || progress[len(progress)-1] != int64(len(content)) {
		t.Errorf("last progress = %v, want %d", progress[max(0, len(progress)-1):], len(content))
	}

	assertText(t, filepath.Join(env.dir, "uploads.json"), "{}")
}

func TestLanesSendsTheFileInOrderToAServerThatDoesNotConfirmLanes(t *testing.T) {
	env, content := setUpLanes(t)
	local := filepath.Join(env.dir, "weights.bin")
	writeFile(t, local, content)

	err := env.client.Upload(env.ctx, local, "weights.bin", UploadOptions{Mode: ModeCreate})

	if err != nil {
		t.Fatal(err)
	}

	assertStored(t, env.fake, "weights.bin", content)

	if env.fake.Joins != 0 {
		t.Errorf("joins = %d, want 0", env.fake.Joins)
	}

	// It made the first lane a whole upload, which is dropped.
	dropped := "DELETE /v1/uploads/" + filestest.Volume + ".0"

	if !slices.Contains(env.fake.Requests, dropped) {
		t.Errorf("requests = %v, want %q among them", env.fake.Requests, dropped)
	}
}

func TestLanesSendsLargeFilesAtOnceWithoutOneLosingItsSavedUploadState(t *testing.T) {
	env, content := setUpLanes(t)
	env.fake.Lanes = true
	writeFile(t, filepath.Join(env.dir, "a.bin"), content)
	writeFile(t, filepath.Join(env.dir, "b.bin"), content)
	original := rename
	var held atomic.Bool

	// The first save of the state file waits before it moves into place, as on a slow disk, so the
	// other upload saves in between.
	rename = func(from, to string) error {
		if strings.HasSuffix(to, "uploads.json") && held.CompareAndSwap(false, true) {
			time.Sleep(50 * time.Millisecond)
		}

		return original(from, to)
	}

	t.Cleanup(func() { rename = original })
	var group sync.WaitGroup
	failures := make([]error, 2)

	for index, name := range []string{"a.bin", "b.bin"} {
		group.Go(func() {
			failures[index] = env.client.Upload(env.ctx, filepath.Join(env.dir, name), name, UploadOptions{Mode: ModeCreate})
		})
	}

	group.Wait()

	if err := errors.Join(failures...); err != nil {
		t.Fatal(err)
	}

	assertStored(t, env.fake, "a.bin", content)
	assertStored(t, env.fake, "b.bin", content)
}

func TestLanesContinuesTheLanesAnEarlierRunLeftUnfinished(t *testing.T) {
	env, content := setUpLanes(t)
	env.fake.Lanes = true
	env.fake.FailPatches = 1_000
	local := filepath.Join(env.dir, "weights.bin")
	writeFile(t, local, content)

	if err := env.client.Upload(env.ctx, local, "weights.bin", UploadOptions{Mode: ModeCreate}); err == nil {
		t.Fatal("the first run succeeded, want it to fail")
	}

	env.fake.Update(func() { env.fake.FailPatches = 0 })

	if err := env.client.Upload(env.ctx, local, "weights.bin", UploadOptions{Mode: ModeCreate}); err != nil {
		t.Fatal(err)
	}

	assertStored(t, env.fake, "weights.bin", content)

	if env.fake.LanesCreated != 4 || env.fake.Joins != 1 {
		t.Errorf("lanes created, joins = %d, %d, want 4, 1", env.fake.LanesCreated, env.fake.Joins)
	}
}

func TestWaitsForAFileServerThatIsStartingAsLongAsItAsks(t *testing.T) {
	env := setUp(t)
	local := filepath.Join(env.dir, "weights.bin")
	content := bytes.Repeat([]byte{5}, 6*1024*1024)
	writeFile(t, local, content)
	env.fake.CutNextPatchAfter = 1_000_000
	env.fake.Starting = map[string]int{"POST": 1, "HEAD": 1}
	var waits []*time.Duration

	client := env.newClient(Options{
		Delay: func(_ int, retryAfter *time.Duration) time.Duration {
			waits = append(waits, retryAfter)
			return time.Millisecond
		},
	})

	err := client.Upload(env.ctx, local, "weights.bin", UploadOptions{Mode: ModeCreate})

	if err != nil {
		t.Fatal(err)
	}

	assertStored(t, env.fake, "weights.bin", content)
	asked := 0

	for _, wait := range waits {
		if wait != nil && *wait == 5*time.Second {
			asked++
		}
	}

	if asked != 2 {
		t.Errorf("waits of 5 s = %d, want 2", asked)
	}
}

func TestGetsANewSessionWhenTheFileServerDidNotStartInTime(t *testing.T) {
	env := setUp(t)
	writeFile(t, filepath.Join(env.dir, "a.txt"), []byte("a"))

	if err := env.client.Upload(env.ctx, filepath.Join(env.dir, "a.txt"), "a.txt", UploadOptions{Mode: ModeCreate}); err != nil {
		t.Fatal(err)
	}

	env.fake.StoppedSinceSession = env.fake.Sessions
	entries, err := env.client.List(env.ctx, "")

	if err != nil {
		t.Fatal(err)
	}

	if names := entryNames(entries); !slices.Equal(names, []string{"a.txt"}) {
		t.Errorf("names = %v, want [a.txt]", names)
	}

	if env.fake.Sessions != 2 {
		t.Errorf("sessions = %d, want 2", env.fake.Sessions)
	}

	env.fake.StoppedSinceSession = env.fake.Sessions
	writeFile(t, filepath.Join(env.dir, "b.bin"), bytes.Repeat([]byte{1}, 6*1024*1024))

	if err := env.client.Upload(env.ctx, filepath.Join(env.dir, "b.bin"), "b.bin", UploadOptions{Mode: ModeCreate}); err != nil {
		t.Fatal(err)
	}

	if file := env.fake.Files["b.bin"]; file == nil || len(file.Data) != 6*1024*1024 {
		t.Errorf("b.bin was not stored whole")
	}

	if env.fake.Sessions != 3 {
		t.Errorf("sessions = %d, want 3", env.fake.Sessions)
	}
}

func entryNames(entries []FileEntry) []string {
	names := []string{}

	for _, entry := range entries {
		names = append(names, entry.Name)
	}

	return names
}

func TestContinuesAnUploadAnEarlierRunLeftUnfinished(t *testing.T) {
	env := setUp(t)
	local := filepath.Join(env.dir, "data.bin")
	content := bytes.Repeat([]byte{9}, 5*1024*1024)
	writeFile(t, local, content)
	info, err := os.Stat(local)

	if err != nil {
		t.Fatal(err)
	}

	mtimeMs := uploadMtimeMs(info)
	milliseconds, _ := strconv.ParseFloat(mtimeMs, 64)
	modifiedAt := time.UnixMilli(int64(milliseconds)).UTC().Format("2006-01-02T15:04:05.000Z")

	env.fake.Uploads[filestest.Volume+".0"] = &filestest.Upload{
		Path: "data.bin", Size: int64(len(content)), Data: bytes.Clone(content[:2_000_000]), Received: 2_000_000, ModifiedAt: modifiedAt,
	}

	key := strings.Join([]string{filestest.Volume, "data.bin", strconv.Itoa(len(content)), mtimeMs, "create", ""}, "\n")
	state := map[string]any{key: map[string]any{"url": env.fake.URL + "/v1/uploads/" + filestest.Volume + ".0", "at": time.Now().UnixMilli()}}
	saved, _ := json.Marshal(state)
	writeFile(t, filepath.Join(env.dir, "uploads.json"), saved)

	if err := env.client.Upload(env.ctx, local, "data.bin", UploadOptions{Mode: ModeCreate}); err != nil {
		t.Fatal(err)
	}

	assertStored(t, env.fake, "data.bin", content)

	if slices.Contains(env.fake.Requests, "POST /v1/uploads") {
		t.Errorf("requests = %v, want no new upload", env.fake.Requests)
	}
}

func TestSendsASmallFileWithTheRequestThatCreatesItKeepingItsModificationTime(t *testing.T) {
	env := setUp(t)
	local := filepath.Join(env.dir, "notes.txt")
	writeFile(t, local, []byte("hello"))
	modified := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	if err := os.Chtimes(local, modified, modified); err != nil {
		t.Fatal(err)
	}

	if err := env.client.Upload(env.ctx, local, "notes.txt", UploadOptions{Mode: ModeCreate}); err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(env.fake.Requests, []string{"POST /v1/uploads"}) {
		t.Errorf("requests = %v, want [POST /v1/uploads]", env.fake.Requests)
	}

	file := env.fake.Files["notes.txt"]

	if file == nil || string(file.Data) != "hello" || file.ModifiedAt != "2026-01-02T03:04:05.000Z" {
		t.Errorf("notes.txt = %+v, want hello modified at 2026-01-02T03:04:05.000Z", file)
	}
}

func TestGuardsAnOverwriteWithTheVersionItRead(t *testing.T) {
	env := setUp(t)
	env.fake.Files["notes.txt"] = filestest.Stored("old", "2026-01-01T00:00:00.000Z", "aaaa1111")
	local := filepath.Join(env.dir, "notes.txt")
	writeFile(t, local, []byte("new"))

	err := env.client.Upload(env.ctx, local, "notes.txt", UploadOptions{Mode: ModeOverwrite, IfMatch: "aaaa1111"})

	if err != nil {
		t.Fatal(err)
	}

	assertStored(t, env.fake, "notes.txt", []byte("new"))
	metadata := env.fake.LastMetadata

	if metadata["mode"] != "overwrite" || metadata["ifMatch"] != `"aaaa1111"` {
		t.Errorf("metadata = %v, want mode overwrite and ifMatch \"aaaa1111\"", metadata)
	}
}

func TestRefusesAnOverwriteWhoseVersionNoLongerMatches(t *testing.T) {
	env := setUp(t)
	env.fake.Files["notes.txt"] = filestest.Stored("old", "2026-01-01T00:00:00.000Z", "aaaa1111")
	local := filepath.Join(env.dir, "notes.txt")
	writeFile(t, local, []byte("new"))

	err := env.client.Upload(env.ctx, local, "notes.txt", UploadOptions{Mode: ModeOverwrite, IfMatch: "bbbb2222"})
	var refusal *FileAPIError

	if !errors.As(err, &refusal) || refusal.Code != "precondition_failed" {
		t.Errorf("error = %v, want a precondition_failed refusal", err)
	}

	assertStored(t, env.fake, "notes.txt", []byte("old"))
}

func TestRefusesAVersionGuardOnACreate(t *testing.T) {
	env := setUp(t)
	local := filepath.Join(env.dir, "notes.txt")
	writeFile(t, local, []byte("new"))

	err := env.client.Upload(env.ctx, local, "notes.txt", UploadOptions{Mode: ModeCreate, IfMatch: "aaaa1111"})

	if err == nil || !strings.Contains(err.Error(), "version guard") {
		t.Errorf("error = %v, want one about the version guard", err)
	}

	if env.fake.Files["notes.txt"] != nil {
		t.Error("notes.txt was stored")
	}
}

func TestReadsAFolderForLeftoverPartialDownloadsOnceNotOncePerFile(t *testing.T) {
	env := setUp(t)
	out := filepath.Join(env.dir, "out")

	if err := os.Mkdir(out, 0o755); err != nil {
		t.Fatal(err)
	}

	// A partial download of another version, left by an earlier run.
	writeFile(t, filepath.Join(out, "a.bin.0123456789ab.partial"), []byte("old"))

	for _, name := range []string{"a.bin", "b.bin", "c.bin"} {
		env.fake.Files[name] = filestest.Stored(name, "2026-02-03T04:05:06.000Z", name+"-version-0000")
	}

	entries, err := env.client.List(env.ctx, "")

	if err != nil {
		t.Fatal(err)
	}

	original := readDir
	var reads atomic.Int32

	readDir = func(name string) ([]os.DirEntry, error) {
		if name == out {
			reads.Add(1)
		}

		return original(name)
	}

	t.Cleanup(func() { readDir = original })

	for _, entry := range entries {
		if err := env.client.Download(env.ctx, entry, filepath.Join(out, entry.Name), nil); err != nil {
			t.Fatal(err)
		}
	}

	if reads.Load() != 1 {
		t.Errorf("reads of the folder = %d, want 1", reads.Load())
	}

	assertFolder(t, out, []string{"a.bin", "b.bin", "c.bin"})
}

func assertFolder(t *testing.T, folder string, want []string) {
	t.Helper()
	children, err := os.ReadDir(folder)

	if err != nil {
		t.Fatal(err)
	}

	names := []string{}

	for _, child := range children {
		names = append(names, child.Name())
	}

	slices.Sort(names)

	if !slices.Equal(names, want) {
		t.Errorf("%s holds %v, want %v", folder, names, want)
	}
}

func TestResumesADroppedDownloadWithARangeAndOnlyThenShowsTheWholeFile(t *testing.T) {
	env := setUp(t)
	content := []byte(strings.Repeat("0123456789", 100_000))
	env.fake.Files["big.txt"] = &filestest.StoredFile{Data: content, ModifiedAt: "2026-02-03T04:05:06.000Z", Version: "abc123def4567890"}
	env.fake.CutNextDownloadAfter = 300_000
	local := filepath.Join(env.dir, "out", "big.txt")
	size := int64(len(content))

	remote := FileEntry{
		Name: "big.txt", Path: "big.txt", Type: TypeFile, SizeBytes: &size, ModifiedAt: "2026-02-03T04:05:06.000Z", Version: "abc123def4567890",
	}

	if err := env.client.Download(env.ctx, remote, local, nil); err != nil {
		t.Fatal(err)
	}

	assertText(t, local, string(content))
	info, err := os.Stat(local)

	if err != nil {
		t.Fatal(err)
	}

	if modified := info.ModTime().UTC().Format("2006-01-02T15:04:05.000Z"); modified != "2026-02-03T04:05:06.000Z" {
		t.Errorf("modified = %s, want 2026-02-03T04:05:06.000Z", modified)
	}

	assertFolder(t, filepath.Join(env.dir, "out"), []string{"big.txt"})
	downloads := countRequests(env.fake.Requests, func(request string) bool { return strings.HasSuffix(request, "/content") })

	if downloads != 2 {
		t.Errorf("content requests = %d, want 2", downloads)
	}
}

func TestRenewsAnExpiredTokenOnceAndFollowsListingPages(t *testing.T) {
	env := setUp(t)

	for _, name := range []string{"a", "b", "c", "d", "e"} {
		env.fake.Files[name] = filestest.Stored(name, "2026-01-01T00:00:00.000Z", name)
	}

	env.fake.ExpireNextToken = true
	entries, err := env.client.List(env.ctx, "")

	if err != nil {
		t.Fatal(err)
	}

	if names := entryNames(entries); !slices.Equal(names, []string{"a", "b", "c", "d", "e"}) {
		t.Errorf("names = %v, want [a b c d e]", names)
	}

	if env.fake.Sessions != 2 {
		t.Errorf("sessions = %d, want 2", env.fake.Sessions)
	}
}

func setUpFind(t *testing.T) *testEnv {
	env := setUp(t)
	env.fake.Files["models/a.bin"] = filestest.Stored("a", "2026-01-02T03:04:05.000Z", "a")
	env.fake.Files["models/sub/b.bin"] = filestest.Stored("bb", "2026-01-02T03:04:05.000Z", "bb")
	env.fake.Files["notes.txt"] = filestest.Stored("n", "2026-01-02T03:04:05.000Z", "n")

	return env
}

// collect finds below folder, returning what came before any failure.
func (e *testEnv) collect(folder string, filters FindFilters) ([]string, error) {
	found := []string{}

	for entry, err := range e.client.Find(e.ctx, folder, filters) {
		if err != nil {
			return found, err
		}

		found = append(found, entry.Path)
	}

	return found, nil
}

func TestFindYieldsEachMatchTheServerStreamsAndEndsOnItsDoneLineSendingTheFilters(t *testing.T) {
	env := setUpFind(t)
	maxDepth := 2
	minSize := int64(0)
	after := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	filters := FindFilters{Name: "*.bin", IgnoreCase: true, Type: TypeFile, MaxDepth: &maxDepth, MinSize: &minSize, ModifiedAfter: &after}
	found, err := env.collect("models", filters)

	if err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(found, []string{"models/a.bin", "models/sub/b.bin"}) {
		t.Errorf("found = %v, want [models/a.bin models/sub/b.bin]", found)
	}

	query := map[string]string{}

	for name, values := range env.fake.FindQuery {
		query[name] = strings.Join(values, ",")
	}

	want := map[string]string{
		"path": "models", "name": "*.bin", "ignoreCase": "true", "type": "file", "maxDepth": "2", "minSize": "0", "modifiedAfter": "2026-01-01T00:00:00.000Z",
	}

	if !reflect.DeepEqual(query, want) {
		t.Errorf("query = %v, want %v", query, want)
	}
}

func TestFindFailsRatherThanPassAStreamThatEndedBeforeDoneForEveryMatch(t *testing.T) {
	env := setUpFind(t)

	for _, end := range []string{"end", "reset"} {
		env.fake.FindEnd = end
		found, err := env.collect("", FindFilters{})

		if !slices.Equal(found, []string{"models/a.bin", "models/sub/b.bin", "notes.txt"}) {
			t.Errorf("%s: found = %v, want all three", end, found)
		}

		if err == nil || err.Error() != "The search stopped before it finished. Run it again." {
			t.Errorf("%s: error = %v, want the search to have stopped", end, err)
		}
	}
}

func TestFindFailsWithTheErrorTheServerSendsAfterItStartedAnswering(t *testing.T) {
	env := setUpFind(t)
	env.fake.FindEnd = "error"
	found, err := env.collect("models", FindFilters{})

	if !slices.Equal(found, []string{"models/a.bin", "models/sub/b.bin"}) {
		t.Errorf("found = %v, want [models/a.bin models/sub/b.bin]", found)
	}

	var refusal *FileAPIError

	if !errors.As(err, &refusal) || refusal.Code != "unavailable" {
		t.Errorf("error = %v, want an unavailable refusal", err)
	}
}

func TestFindFollowsTheCursorsOfPagesTheServerStopsEarly(t *testing.T) {
	env := setUpFind(t)
	env.fake.FindPageAt = 1
	found, err := env.collect("", FindFilters{})

	if err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(found, []string{"models/a.bin", "models/sub/b.bin", "notes.txt"}) {
		t.Errorf("found = %v, want all three", found)
	}
}

func TestFindGivesUpWhenStoppedPagesKeepComingWithoutACursor(t *testing.T) {
	env := setUpFind(t)
	env.fake.FindPageAt = 1
	env.fake.FindStall = "no-cursor"
	_, err := env.collect("", FindFilters{})

	if err == nil || !strings.Contains(err.Error(), "kept stopping") {
		t.Errorf("error = %v, want the search to have kept stopping", err)
	}
}

func TestFindGivesUpWhenTheServerKeepsRepeatingTheSameCursor(t *testing.T) {
	env := setUpFind(t)
	env.fake.FindPageAt = 1
	env.fake.FindStall = "same-cursor"
	_, err := env.collect("", FindFilters{})

	if err == nil || !strings.Contains(err.Error(), "kept stopping") {
		t.Errorf("error = %v, want the search to have kept stopping", err)
	}
}
