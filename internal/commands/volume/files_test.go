package volume

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"featherless.ai/featherless-cloud/internal/api"
	"featherless.ai/featherless-cloud/internal/completion"
	"featherless.ai/featherless-cloud/internal/files"
	"featherless.ai/featherless-cloud/internal/files/filestest"
	"featherless.ai/featherless-cloud/internal/output"
)

type testEnv struct {
	ctx    context.Context
	fake   *filestest.Server
	dir    string
	client *files.Client
}

func setUp(t *testing.T) *testEnv {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)

	env := &testEnv{ctx: ctx, fake: filestest.Start(t), dir: t.TempDir()}
	env.client = env.newClient(filestest.Volume)

	return env
}

// newClient retries after a millisecond and keeps its upload state in the test folder.
func (e *testEnv) newClient(volumeID string) *files.Client {
	options := files.Options{
		Delay:     func(int, *time.Duration) time.Duration { return time.Millisecond },
		StateFile: filepath.Join(e.dir, "uploads.json"),
	}

	return files.NewClient(e.fake, volumeID, options)
}

func (e *testEnv) data() volumeFiles {
	return volumeFiles{Files: e.client, VolumeName: "data"}
}

// lockedBuffer collects what transfers running at once write.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func (b *lockedBuffer) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf.Reset()
}

// newOutput is an output context in mode whose writes the test reads.
func newOutput(mode output.Mode) (*output.Context, *lockedBuffer, *lockedBuffer) {
	stdout := &lockedBuffer{}
	stderr := &lockedBuffer{}
	out := output.New(mode)
	out.Stdout = stdout
	out.Stderr = stderr

	return out, stdout, stderr
}

func expect[T comparable](t *testing.T, what string, got, want T) {
	t.Helper()

	if got != want {
		t.Errorf("%s = %v, want %v", what, got, want)
	}
}

func expectError(t *testing.T, err error, part string) {
	t.Helper()

	if err == nil || !strings.Contains(err.Error(), part) {
		t.Errorf("error = %v, want one containing %q", err, part)
	}
}

func mustAddress(t *testing.T, value string) address {
	t.Helper()
	parsed, err := parseAddress(value)

	if err != nil {
		t.Fatal(err)
	}

	if parsed == nil {
		t.Fatalf("%s is not a volume address", value)
	}

	return *parsed
}

func writeFile(t *testing.T, path, text string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// localText is a local file's content, or "<missing>".
func localText(path string) string {
	content, err := os.ReadFile(path)

	if err != nil {
		return "<missing>"
	}

	return string(content)
}

func localExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// remoteText is a stored file's content, or "<missing>".
func remoteText(volume map[string]*filestest.StoredFile, path string) string {
	file := volume[path]

	if file == nil {
		return "<missing>"
	}

	return string(file.Data)
}

func sortedKeys[V any](items map[string]V) []string {
	keys := []string{}

	for key := range items {
		keys = append(keys, key)
	}

	slices.Sort(keys)

	return keys
}

func sources(items []transfer) []string {
	values := []string{}

	for _, item := range items {
		values = append(values, item.Source)
	}

	return values
}

var (
	datasets = api.NetworkVolume{ID: "11111111-2222-4333-8444-555555555555", Name: "datasets"}
	models   = api.NetworkVolume{ID: "22222222-2222-4333-8444-555555555555", Name: "models"}
	myData   = api.NetworkVolume{ID: "33333333-2222-4333-8444-555555555555", Name: "my data"}
)

// fakeCloud lists the account's volumes.
type fakeCloud struct {
	api.Cloud
	volumes []api.NetworkVolume
}

func (f *fakeCloud) ListVolumes(ctx context.Context) ([]api.NetworkVolume, error) {
	return f.volumes, nil
}

func withVolumes(volumes ...api.NetworkVolume) *fakeCloud {
	return &fakeCloud{volumes: volumes}
}

// completionContext hands out cloud, counting how often a completer asked for it.
func completionContext(cloud api.Cloud) (*completion.Context, *int) {
	calls := 0

	completing := &completion.Context{
		Cloud: func() (api.Cloud, error) {
			calls++
			return cloud, nil
		},
	}

	return completing, &calls
}

func complete(t *testing.T, word string, cloud api.Cloud) []string {
	t.Helper()
	completing, _ := completionContext(cloud)
	candidates, err := Paths(word, completing)

	if err != nil {
		t.Fatalf("Paths(%q): %v", word, err)
	}

	return candidates
}

func expectCandidates(t *testing.T, word string, got, want []string) {
	t.Helper()

	if !slices.Equal(got, want) {
		t.Errorf("Paths(%q) = %q, want %q", word, got, want)
	}
}

// listFolders answers folder listings from list, called with the volume id and folder.
func listFolders(t *testing.T, list func(volumeID, folder string) ([]files.FileEntry, error)) {
	original := listFolder

	listFolder = func(ctx context.Context, cloud api.Cloud, volumeID, folder string) ([]files.FileEntry, error) {
		return list(volumeID, folder)
	}

	t.Cleanup(func() { listFolder = original })
}

func folderHolds(t *testing.T, entries ...files.FileEntry) {
	listFolders(t, func(string, string) ([]files.FileEntry, error) { return entries, nil })
}

func dirEntry(name, path string) files.FileEntry {
	return files.FileEntry{Name: name, Path: path, Type: files.TypeDirectory, ModifiedAt: "2026-01-01T00:00:00Z"}
}

func fileEntry(name, path string) files.FileEntry {
	size := int64(10)
	return files.FileEntry{Name: name, Path: path, Type: files.TypeFile, SizeBytes: &size, ModifiedAt: "2026-01-01T00:00:00Z"}
}

func TestVolumePathsCompletesVolumeNamesByCaseInsensitivePrefixAndUUIDPrefix(t *testing.T) {
	folderHolds(t)
	cloud := withVolumes(datasets, models)

	expectCandidates(t, "vol://da", complete(t, "vol://da", cloud), []string{"vol://datasets/"})
	expectCandidates(t, "vol://DAT", complete(t, "vol://DAT", cloud), []string{"vol://datasets/"})
	expectCandidates(t, "vol://22222222", complete(t, "vol://22222222", cloud), []string{"vol://models/"})
}

func TestVolumePathsListsAllVolumesWithATrailingSlashAtTheScheme(t *testing.T) {
	folderHolds(t)

	got := complete(t, "vol://", withVolumes(datasets, models))
	expectCandidates(t, "vol://", got, []string{"vol://datasets/", "vol://models/"})
}

func TestVolumePathsCompletesPartialSchemePrefixesToVol(t *testing.T) {
	folderHolds(t)

	expectCandidates(t, "vol", complete(t, "vol", withVolumes()), []string{"vol://"})
	expectCandidates(t, "vol:", complete(t, "vol:", withVolumes()), []string{"vol://"})
	expectCandidates(t, "v", complete(t, "v", withVolumes()), []string{})
}

func TestVolumePathsListsFolderEntriesMarkingDirectoriesWithASlash(t *testing.T) {
	folderHolds(t, fileEntry("a.txt", "a.txt"), dirEntry("sub", "sub"))

	got := complete(t, "vol://datasets/", withVolumes(datasets))
	expectCandidates(t, "vol://datasets/", got, []string{"vol://datasets/a.txt", "vol://datasets/sub/"})
}

func TestVolumePathsFiltersByTheInProgressNameCaseSensitively(t *testing.T) {
	cloud := withVolumes(datasets)
	folderHolds(t, fileEntry("a.txt", "a.txt"), fileEntry("A.md", "A.md"), dirEntry("sub", "sub"))

	expectCandidates(t, "vol://datasets/a", complete(t, "vol://datasets/a", cloud), []string{"vol://datasets/a.txt"})
	expectCandidates(t, "vol://datasets/s", complete(t, "vol://datasets/s", cloud), []string{"vol://datasets/sub/"})
}

func TestVolumePathsListsNestedFoldersUsingTheCanonicalVolumeName(t *testing.T) {
	var calls [][2]string

	listFolders(t, func(volumeID, folder string) ([]files.FileEntry, error) {
		calls = append(calls, [2]string{volumeID, folder})
		return []files.FileEntry{fileEntry("x.bin", "train/x.bin")}, nil
	})

	got := complete(t, "vol://dat/train/", withVolumes(datasets))
	expectCandidates(t, "vol://dat/train/", got, []string{"vol://datasets/train/x.bin"})

	if !slices.Contains(calls, [2]string{datasets.ID, "train"}) {
		t.Errorf("listings = %v, want %s train among them", calls, datasets.ID)
	}
}

func TestVolumePathsCapsListingAtThreeMatchingVolumes(t *testing.T) {
	calls := 0

	listFolders(t, func(string, string) ([]files.FileEntry, error) {
		calls++
		return []files.FileEntry{fileEntry("x", "x")}, nil
	})

	many := []api.NetworkVolume{{ID: "id-1", Name: "data1"}, {ID: "id-2", Name: "data2"}, {ID: "id-3", Name: "data3"}, {ID: "id-4", Name: "data4"}}
	got := complete(t, "vol://data/x", withVolumes(many...))

	expectCandidates(t, "vol://data/x", got, []string{"vol://data1/x", "vol://data2/x", "vol://data3/x"})
	expect(t, "listings", calls, 3)
}

func TestVolumePathsIgnoresVolumesWhoseListingFails(t *testing.T) {
	listFolders(t, func(volumeID, folder string) ([]files.FileEntry, error) {
		if volumeID == datasets.ID {
			return nil, errors.New("forbidden")
		}

		return []files.FileEntry{fileEntry("ok.txt", "ok.txt")}, nil
	})

	got := complete(t, "vol://dat/", withVolumes(datasets, api.NetworkVolume{ID: "x", Name: "datax"}))
	expectCandidates(t, "vol://dat/", got, []string{"vol://datax/ok.txt"})
}

func TestVolumePathsReturnsNothingForLocalWordsWithoutContactingTheAPI(t *testing.T) {
	folderHolds(t)
	completing, calls := completionContext(withVolumes(datasets))

	for _, word := range []string{"./weights", "vo"} {
		got, err := Paths(word, completing)

		if err != nil {
			t.Fatal(err)
		}

		expectCandidates(t, word, got, []string{})
	}

	expect(t, "cloud calls", *calls, 0)
}

func TestVolumePathsCompletesVolumeAndEntryNamesContainingSpacesWithRawSpaces(t *testing.T) {
	folderHolds(t)
	expectCandidates(t, "vol://my", complete(t, "vol://my", withVolumes(myData, datasets)), []string{"vol://my data/"})

	folderHolds(t, dirEntry("my data", "my data"))
	got := complete(t, "vol://datasets/my", withVolumes(datasets))
	expectCandidates(t, "vol://datasets/my", got, []string{"vol://datasets/my data/"})

	folderHolds(t, fileEntry("report 2024.txt", "report 2024.txt"))
	got = complete(t, "vol://datasets/report", withVolumes(datasets))
	expectCandidates(t, "vol://datasets/report", got, []string{"vol://datasets/report 2024.txt"})
}

func TestVolumePathsDeDuplicatesIdenticalCandidates(t *testing.T) {
	folderHolds(t, fileEntry("x", "x"))

	got := complete(t, "vol://data/", withVolumes(datasets, api.NetworkVolume{ID: "y", Name: "datax"}))
	expectCandidates(t, "vol://data/", got, []string{"vol://datasets/x", "vol://datax/x"})
}

func setUpFind(t *testing.T) *testEnv {
	env := setUp(t)
	env.fake.Files["models/a.bin"] = filestest.Stored("a", "2026-01-02T03:04:05.000Z", "a")
	env.fake.Files["models/sub/b.bin"] = filestest.Stored("bb", "2026-01-02T03:04:05.000Z", "bb")
	env.fake.Files["notes.txt"] = filestest.Stored("n", "2026-01-02T03:04:05.000Z", "n")

	return env
}

func TestFindPrintsVolAddressesAsTheyComeNULSeparatedWithPrint0AndAListingWithLong(t *testing.T) {
	env := setUpFind(t)
	terminal, stdout, _ := newOutput(output.Interactive)
	folder := mustAddress(t, "vol://data/models")

	if err := find(env.ctx, terminal, env.data(), folder, files.FindFilters{}, findOptions{}); err != nil {
		t.Fatal(err)
	}

	expect(t, "stdout", stdout.String(), "vol://data/models/a.bin\nvol://data/models/sub/b.bin\n")
	stdout.Reset()

	if err := find(env.ctx, terminal, env.data(), folder, files.FindFilters{}, findOptions{Print0: true}); err != nil {
		t.Fatal(err)
	}

	expect(t, "stdout", stdout.String(), "vol://data/models/a.bin\x00vol://data/models/sub/b.bin\x00")
	stdout.Reset()

	if err := find(env.ctx, terminal, env.data(), folder, files.FindFilters{}, findOptions{Long: true}); err != nil {
		t.Fatal(err)
	}

	one := int64(1)
	two := int64(2)

	lines := listing([]listingLine{
		{At: "2026-01-02T03:04:05.000Z", Bytes: &one, Name: "vol://data/models/a.bin"},
		{At: "2026-01-02T03:04:05.000Z", Bytes: &two, Name: "vol://data/models/sub/b.bin"},
	}, false)

	expect(t, "stdout", stdout.String(), strings.Join(lines, "\n")+"\n")
}

func TestCpAndMvCopiesIntoAFolderUnderTheSourceNameAndOntoTheDestinationNameOtherwise(t *testing.T) {
	env := setUp(t)
	out, _, _ := newOutput(output.JSON)
	a := filepath.Join(env.dir, "a.txt")
	c := filepath.Join(env.dir, "c.txt")
	writeFile(t, a, "a")
	writeFile(t, c, "c")

	copies := []struct {
		sources     []string
		destination string
	}{
		{[]string{a}, "vol://data/models/"},
		{[]string{c}, "vol://data/models"},
		{[]string{a}, "vol://data/b.txt"},
	}

	for _, copied := range copies {
		_, err := upload(env.ctx, out, env.data(), copied.sources, mustAddress(t, copied.destination), copyOptions{Limit: 1})

		if err != nil {
			t.Fatal(err)
		}
	}

	if keys := sortedKeys(env.fake.Files); !slices.Equal(keys, []string{"b.txt", "models/a.txt", "models/c.txt"}) {
		t.Errorf("stored = %v, want [b.txt models/a.txt models/c.txt]", keys)
	}

	_, err := upload(env.ctx, out, env.data(), []string{a, c}, mustAddress(t, "vol://data/new"), copyOptions{Limit: 1})
	expectError(t, err, "not a folder")
}

func TestCpAndMvAMoveDeletesEachSourceOnlyOnceItsOwnCopyFinished(t *testing.T) {
	env := setUp(t)
	out, _, _ := newOutput(output.JSON)
	kept := filepath.Join(env.dir, "kept.txt")
	moved := filepath.Join(env.dir, "moved.txt")
	writeFile(t, kept, "local")
	writeFile(t, moved, "moved")
	env.fake.Files["kept.txt"] = filestest.Stored("remote", "2026-01-01T00:00:00.000Z", "k")

	up, err := upload(env.ctx, out, env.data(), []string{kept, moved}, mustAddress(t, "vol://data/"), copyOptions{Limit: 1, Move: true})

	if err != nil {
		t.Fatal(err)
	}

	if failed := sources(up.Failed); !slices.Equal(failed, []string{kept}) {
		t.Errorf("failed = %v, want [%s]", failed, kept)
	}

	expect(t, "kept.txt", localText(kept), "local")
	expect(t, "moved.txt still here", localExists(moved), false)
	expect(t, "remote moved.txt", remoteText(env.fake.Files, "moved.txt"), "moved")

	into := filepath.Join(env.dir, "out")

	if err := os.Mkdir(into, 0o755); err != nil {
		t.Fatal(err)
	}

	down, err := download(env.ctx, out, env.data(), []address{mustAddress(t, "vol://data/moved.txt")}, into, copyOptions{Limit: 1, Move: true})

	if err != nil {
		t.Fatal(err)
	}

	if done := sources(down.Done); !slices.Equal(done, []string{"vol://data/moved.txt"}) {
		t.Errorf("done = %v, want [vol://data/moved.txt]", done)
	}

	expect(t, "out/moved.txt", localText(filepath.Join(into, "moved.txt")), "moved")
	expect(t, "remote moved.txt kept", env.fake.Files["moved.txt"] != nil, false)
}

func TestCpAndMvPrintsNothingOnSuccessEachFileWithVerboseAndEachFailureOnStderr(t *testing.T) {
	env := setUp(t)
	terminal, stdout, stderr := newOutput(output.Interactive)
	a := filepath.Join(env.dir, "a.txt")
	writeFile(t, a, "a")

	if _, err := upload(env.ctx, terminal, env.data(), []string{a}, mustAddress(t, "vol://data/"), copyOptions{Limit: 1}); err != nil {
		t.Fatal(err)
	}

	expect(t, "output", stdout.String()+stderr.String(), "")

	if _, err := upload(env.ctx, terminal, env.data(), []string{a}, mustAddress(t, "vol://data/b.txt"), copyOptions{Limit: 1, Verbose: true}); err != nil {
		t.Fatal(err)
	}

	expect(t, "stdout", stdout.String(), a+" -> vol://data/b.txt\n")
	report, err := upload(env.ctx, terminal, env.data(), []string{a}, mustAddress(t, "vol://data/"), copyOptions{Limit: 1})

	if err != nil {
		t.Fatal(err)
	}

	expect(t, "failures", len(report.Failed), 1)
	expect(t, "stderr", stderr.String(), a+": already exists in the volume (pass --overwrite or --update)\n")
}

func TestCpAndMvShowsProgressOnStderrOnlyWhenAskedEndingWithOneFinishedLine(t *testing.T) {
	env := setUp(t)
	terminal, _, stderr := newOutput(output.Interactive)
	a := filepath.Join(env.dir, "a.txt")
	writeFile(t, a, "a")
	var mu sync.Mutex
	now := time.Now()

	terminal.Now = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		now = now.Add(6 * time.Second)
		return now
	}

	if _, err := upload(env.ctx, terminal, env.data(), []string{a}, mustAddress(t, "vol://data/"), copyOptions{Limit: 1, Progress: true}); err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(strings.TrimSuffix(stderr.String(), "\n"), "\n")

	if !strings.HasPrefix(lines[0], "Uploading 0/1 files, 0 B of 1 B") {
		t.Errorf("first line = %q, want it to start Uploading 0/1 files, 0 B of 1 B", lines[0])
	}

	finished := 0

	for _, line := range lines {
		if strings.HasPrefix(line, "Uploading 1/1 files, 1 B of 1 B") {
			finished++
		}
	}

	expect(t, "finished lines", finished, 1)
}

func setUpBetween(t *testing.T) (*testEnv, volumeFiles) {
	env := setUp(t)
	backup := volumeFiles{Files: env.newClient(filestest.Backup), VolumeName: "backup"}
	env.fake.Files["models/a.bin"] = filestest.Stored("a", "2026-01-02T03:04:05.000Z", "a")
	env.fake.Files["models/sub/b.bin"] = filestest.Stored("b", "2026-01-02T03:04:05.000Z", "b")

	return env, backup
}

func TestBetweenVolumesCopiesAFolderOnTheFileServerKeepingItsLayoutAndNeverThroughThisComputer(t *testing.T) {
	env, backup := setUpBetween(t)
	out, _, _ := newOutput(output.JSON)
	from := []address{mustAddress(t, "vol://data/models")}

	report, err := between(env.ctx, out, env.data(), from, backup, mustAddress(t, "vol://backup/"), copyOptions{Limit: 2, Recursive: true})

	if err != nil {
		t.Fatal(err)
	}

	targets := []string{}

	for _, item := range report.Done {
		targets = append(targets, item.Target)
	}

	slices.Sort(targets)

	if !slices.Equal(targets, []string{"vol://backup/models/a.bin", "vol://backup/models/sub/b.bin"}) {
		t.Errorf("targets = %v, want the folder's two files in backup", targets)
	}

	if keys := sortedKeys(env.fake.Backup); !slices.Equal(keys, []string{"models/a.bin", "models/sub/b.bin"}) {
		t.Errorf("backup holds %v, want [models/a.bin models/sub/b.bin]", keys)
	}

	expect(t, "files in data", len(env.fake.Files), 2)

	for _, request := range env.fake.Requests {
		if strings.Contains(request, "/uploads") || strings.Contains(request, "/content") {
			t.Errorf("request %q passed the files through this computer", request)
		}
	}

	if copied := "POST /v1/volumes/" + filestest.Backup + "/copy"; !slices.Contains(env.fake.Requests, copied) {
		t.Errorf("requests = %v, want %q among them", env.fake.Requests, copied)
	}
}

func TestBetweenVolumesMovesDeletingEachSourceOnlyOnceItsCopyFinishedAndSkipsUnchangedFilesWithUpdate(t *testing.T) {
	env, backup := setUpBetween(t)
	out, _, _ := newOutput(output.JSON)
	env.fake.Backup["a.bin"] = filestest.Stored("a", "2026-01-02T03:04:05.000Z", "a")
	from := []address{mustAddress(t, "vol://data/models/a.bin")}

	skipped, err := between(env.ctx, out, env.data(), from, backup, mustAddress(t, "vol://backup/"), copyOptions{Limit: 1, Update: true})

	if err != nil {
		t.Fatal(err)
	}

	if got := sources(skipped.Skipped); !slices.Equal(got, []string{"vol://data/models/a.bin"}) {
		t.Errorf("skipped = %v, want [vol://data/models/a.bin]", got)
	}

	env.fake.Files["models/c.bin"] = filestest.Stored("c", "2026-01-02T03:04:05.000Z", "c")
	env.fake.Backup["models/c.bin"] = filestest.Stored("other", "2026-01-02T03:04:05.000Z", "other")
	from = []address{mustAddress(t, "vol://data/models")}

	report, err := between(env.ctx, out, env.data(), from, backup, mustAddress(t, "vol://backup/"), copyOptions{Limit: 1, Move: true})

	if err != nil {
		t.Fatal(err)
	}

	failed := [][2]string{}

	for _, item := range report.Failed {
		failed = append(failed, [2]string{item.Source, item.Error})
	}

	want := [][2]string{{"vol://data/models/c.bin", "already exists in the destination (pass --overwrite)"}}

	if !slices.Equal(failed, want) {
		t.Errorf("failed = %v, want %v", failed, want)
	}

	if keys := sortedKeys(env.fake.Files); !slices.Equal(keys, []string{"models/c.bin"}) {
		t.Errorf("data holds %v, want [models/c.bin]", keys)
	}

	expect(t, "backup models/sub/b.bin", remoteText(env.fake.Backup, "models/sub/b.bin"), "b")

	if moved := "POST /v1/volumes/" + filestest.Backup + "/move"; !slices.Contains(env.fake.Requests, moved) {
		t.Errorf("requests = %v, want %q among them", env.fake.Requests, moved)
	}
}

func TestBetweenVolumesReportsAFailureTheServerSendsAfterItStartedAnswering(t *testing.T) {
	env, backup := setUpBetween(t)
	out, _, _ := newOutput(output.JSON)
	env.fake.FailNextCopyLate = "volume_full"
	from := []address{mustAddress(t, "vol://data/models/a.bin")}

	report, err := between(env.ctx, out, env.data(), from, backup, mustAddress(t, "vol://backup/a.bin"), copyOptions{Limit: 1})

	if err != nil {
		t.Fatal(err)
	}

	failures := []string{}

	for _, item := range report.Failed {
		failures = append(failures, item.Error)
	}

	if !slices.Equal(failures, []string{"The volume is full."}) {
		t.Errorf("failures = %v, want [The volume is full.]", failures)
	}

	expect(t, "files in backup", len(env.fake.Backup), 0)
}

func TestListingPrintsLinesLikeAwsS3LsLocalTimeSizeWithFoldersBlankName(t *testing.T) {
	at := time.Date(2026, time.October, 6, 21, 1, 30, 0, time.Local).UTC().Format("2006-01-02T15:04:05.000Z")
	stamp := "2026-10-06 21:01:30"
	small := int64(2)
	big := int64(104_857_600)

	lines := []listingLine{
		{At: at, Name: "./"},
		{At: at, Name: "sub/"},
		{At: at, Bytes: &small, Name: "b.txt"},
		{At: at, Bytes: &big, Name: "big.bin"},
	}

	exact := []string{
		stamp + strings.Repeat(" ", 13) + "./",
		stamp + strings.Repeat(" ", 13) + "sub/",
		stamp + strings.Repeat(" ", 10) + "2  b.txt",
		stamp + "  104857600  big.bin",
	}

	if got := listing(lines, false); !slices.Equal(got, exact) {
		t.Errorf("listing = %q, want %q", got, exact)
	}

	human := []string{
		stamp + strings.Repeat(" ", 13) + "./",
		stamp + strings.Repeat(" ", 13) + "sub/",
		stamp + strings.Repeat(" ", 8) + "2 B  b.txt",
		stamp + "  100.0 MiB  big.bin",
	}

	if got := listing(lines, true); !slices.Equal(got, human) {
		t.Errorf("human listing = %q, want %q", got, human)
	}
}

func TestProgressLineCountsFilesAndBytesWithTheRateOnceASecondHasPassed(t *testing.T) {
	gib := float64(1 << 30)
	done := count{Files: 12, Bytes: int64(1.2 * gib)}
	total := count{Files: 40, Bytes: 4 << 30}
	got := progressLine("Uploading", done, total, 14_400*time.Millisecond)
	expect(t, "line", got, "Uploading 12/40 files, 1.2 GiB of 4.0 GiB, 85.3 MiB/s")

	got = progressLine("Downloading", count{}, count{Files: 3, Bytes: 2048}, 0)
	expect(t, "line", got, "Downloading 0/3 files, 0 B of 2.0 KiB")
}

func TestParseAddressReadsVolVolumePathAndLeavesEverythingElseLocal(t *testing.T) {
	for _, local := range []string{"./weights", `C:\data`} {
		parsed, err := parseAddress(local)

		if parsed != nil || err != nil {
			t.Errorf("parseAddress(%q) = %v, %v, want nil", local, parsed, err)
		}
	}

	want := map[string]address{
		"vol://data":               {Volume: "data", Path: "", Folder: true},
		"vol://data/models/":       {Volume: "data", Path: "models", Folder: true},
		"vol://data//models/a.bin": {Volume: "data", Path: "models/a.bin", Folder: false},
	}

	for value, expected := range want {
		parsed, err := parseAddress(value)

		if err != nil || parsed == nil || *parsed != expected {
			t.Errorf("parseAddress(%q) = %v, %v, want %+v", value, parsed, err, expected)
		}
	}

	_, err := parseAddress("vol://data/a/../b")
	expectError(t, err, "not a valid volume path")
}

func TestDirectionCopiesTowardWhicheverSideIsAVolume(t *testing.T) {
	up, err := direction([]string{"./a", "./b", "vol://v/x/"})

	if err != nil {
		t.Fatal(err)
	}

	expect(t, "kind", up.Kind, planUpload)

	if !slices.Equal(up.LocalSources, []string{"./a", "./b"}) {
		t.Errorf("sources = %v, want [./a ./b]", up.LocalSources)
	}

	expect(t, "destination volume", up.VolumeDestination.Volume, "v")
	expect(t, "destination path", up.VolumeDestination.Path, "x")
	down, err := direction([]string{"vol://v/a", "."})

	if err != nil {
		t.Fatal(err)
	}

	expect(t, "kind", down.Kind, planDownload)

	if len(down.VolumeSources) != 1 || down.VolumeSources[0].Volume != "v" || down.VolumeSources[0].Path != "a" {
		t.Errorf("sources = %+v, want v a", down.VolumeSources)
	}

	expect(t, "destination", down.LocalDestination, ".")
	within, err := direction([]string{"vol://v/a", "vol://v/b"})

	if err != nil {
		t.Fatal(err)
	}

	expect(t, "kind", within.Kind, planWithin)
}

func TestDirectionRefusesWhatIsNotACopyBetweenThisComputerAndAVolume(t *testing.T) {
	refusals := []struct {
		paths []string
		part  string
	}{
		{[]string{"vol://v/a"}, "a source and a destination"},
		{[]string{"./a", "./b"}, "Neither side is a volume"},
		{[]string{"./a", "vol://v/a", "vol://v/"}, "all on this computer or all in a volume"},
		{[]string{"vol:///a", "."}, "not a volume path"},
	}

	for _, refusal := range refusals {
		_, err := direction(refusal.paths)
		expectError(t, err, refusal.part)
	}
}

func TestUnderKeepsTheSourceNameInsideAFolderAndCopiesAVolumeRootAsItsContents(t *testing.T) {
	expect(t, "under(true, weights, a/b.bin)", under(true, "weights", "a/b.bin"), "weights/a/b.bin")
	expect(t, "under(false, weights, a/b.bin)", under(false, "weights", "a/b.bin"), "a/b.bin")
	expect(t, "under(false, a.bin, '')", under(false, "a.bin", ""), "")
	expect(t, "under(true, '', a/b.bin)", under(true, "", "a/b.bin"), "a/b.bin")
}

func TestRemotePathDropsTheSlashesPeopleTypeAndRefusesDotSegments(t *testing.T) {
	cleaned, err := remotePath("/data//models/")

	if err != nil {
		t.Fatal(err)
	}

	expect(t, "remotePath(/data//models/)", cleaned, "data/models")
	empty, err := remotePath("")

	if err != nil {
		t.Fatal(err)
	}

	expect(t, "remotePath('')", empty, "")
	_, err = remotePath("data/../other")
	expectError(t, err, "not a valid volume path")
}

func sizeBounds(t *testing.T, values ...string) (minSize, maxSize int64) {
	t.Helper()
	bounds, err := parseSizes(values)

	if err != nil {
		t.Fatal(err)
	}

	minSize = -1
	maxSize = -1

	if bounds.MinSize != nil {
		minSize = *bounds.MinSize
	}

	if bounds.MaxSize != nil {
		maxSize = *bounds.MaxSize
	}

	return minSize, maxSize
}

func TestParseSizesReadsFindSizeBoundsIn1024BasedUnitsKeepingTheNarrowestRange(t *testing.T) {
	// -1 stands for a bound left unset.
	cases := []struct {
		values   []string
		min, max int64
	}{
		{nil, -1, -1},
		{[]string{"+10G"}, 10 << 30, -1},
		{[]string{"-512k", "+1.5MiB", "+100"}, 1_572_864, 512 << 10},
		{[]string{"-2G", "-1G"}, -1, 1 << 30},
	}

	for _, bound := range cases {
		minSize, maxSize := sizeBounds(t, bound.values...)

		if minSize != bound.min || maxSize != bound.max {
			t.Errorf("parseSizes(%q) = %d, %d, want %d, %d", bound.values, minSize, maxSize, bound.min, bound.max)
		}
	}

	for _, value := range []string{"10G", "+10X"} {
		_, err := parseSizes([]string{value})
		expectError(t, err, "not a size bound")
	}
}

func TestParseAgeReadsANumberAndAUnit(t *testing.T) {
	ages := map[string]time.Duration{"30m": 30 * time.Minute, "1.5h": 90 * time.Minute, "7d": 7 * 24 * time.Hour}

	for value, want := range ages {
		got, err := parseAge(value)

		if err != nil || got != want {
			t.Errorf("parseAge(%q) = %v, %v, want %v", value, got, err, want)
		}
	}

	for _, value := range []string{"7", "yesterday"} {
		_, err := parseAge(value)
		expectError(t, err, "not an age")
	}
}
