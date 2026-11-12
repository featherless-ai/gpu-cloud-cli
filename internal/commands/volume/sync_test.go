package volume

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"featherless.ai/featherless-cloud/internal/files/filestest"
	"featherless.ai/featherless-cloud/internal/output"
)

type syncEnv struct {
	*testEnv
	local string
	out   *output.Context
}

func setUpSync(t *testing.T) *syncEnv {
	env := setUp(t)
	// Sync keeps what it last saw below the config folder, which comes from HOME.
	t.Setenv("HOME", env.dir)
	out, _, _ := newOutput(output.JSON)

	return &syncEnv{testEnv: env, local: filepath.Join(env.dir, "local"), out: out}
}

func (e *syncEnv) put(t *testing.T, name, text, at string) {
	t.Helper()
	path := filepath.Join(e.local, name)
	writeFile(t, path, text)
	modified, err := time.Parse(time.RFC3339, at)

	if err != nil {
		t.Fatal(err)
	}

	if err := os.Chtimes(path, modified, modified); err != nil {
		t.Fatal(err)
	}
}

func (e *syncEnv) sync(local, remoteBase string, options syncOptions) (syncReport, error) {
	return syncVolumeFolder(e.ctx, e.out, e.data(), local, remoteBase, options)
}

func (e *syncEnv) mustSync(t *testing.T, local, remoteBase string, options syncOptions) syncReport {
	t.Helper()
	report, err := e.sync(local, remoteBase, options)

	if err != nil {
		t.Fatal(err)
	}

	return report
}

func symlink(t *testing.T, target, link string) {
	t.Helper()

	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

func TestSyncSyncsAFolderReachedThroughASymlinkedParentRefusingASymlinkedFolderItself(t *testing.T) {
	env := setUpSync(t)
	env.put(t, "a.txt", "x", "2026-02-01T00:00:00Z")
	symlink(t, env.dir, filepath.Join(env.dir, "alias"))

	report := env.mustSync(t, filepath.Join(env.dir, "alias", "local"), "", syncOptions{})
	expect(t, "uploaded", report.Uploaded, 1)

	symlink(t, env.local, filepath.Join(env.dir, "link"))
	_, err := env.sync(filepath.Join(env.dir, "link"), "", syncOptions{})
	expectError(t, err, "not a real folder")
}

func TestSyncSendsNewerLocalFilesAndReceivesNewerRemoteOnes(t *testing.T) {
	env := setUpSync(t)
	env.put(t, "a.txt", "new a", "2026-02-01T00:00:00Z")
	env.fake.Files["a.txt"] = filestest.Stored("old a", "2026-01-01T00:00:00.000Z", "v1")
	env.fake.Files["b.bin"] = filestest.Stored("b data", "2026-01-05T00:00:00.000Z", "v2")

	report := env.mustSync(t, env.local, "", syncOptions{})

	expect(t, "uploaded", report.Uploaded, 1)
	expect(t, "downloaded", report.Downloaded, 1)
	expect(t, "conflicts", report.Conflicts, 0)
	// The replacement was guarded by the version the listing read.
	expect(t, "mode", env.fake.LastMetadata["mode"], "overwrite")
	expect(t, "ifMatch", env.fake.LastMetadata["ifMatch"], `"v1"`)
	// Each side now holds both files, with their own modification times kept.
	expect(t, "remote a.txt", remoteText(env.fake.Files, "a.txt"), "new a")

	if file := env.fake.Files["a.txt"]; file != nil {
		expect(t, "remote a.txt modified", file.ModifiedAt, "2026-02-01T00:00:00.000Z")
	}

	expect(t, "local b.bin", localText(filepath.Join(env.local, "b.bin")), "b data")
	info, err := os.Stat(filepath.Join(env.local, "b.bin"))

	if err != nil {
		t.Fatal(err)
	}

	expect(t, "local b.bin modified", info.ModTime().UTC().Format("2006-01-02T15:04:05.000Z"), "2026-01-05T00:00:00.000Z")
}

func TestSyncPairsNestedFoldersByNameSendingAndReceivingBelowTheRoot(t *testing.T) {
	env := setUpSync(t)
	env.put(t, "sub/a.txt", "new a", "2026-02-01T00:00:00Z")
	env.put(t, "sub/b.bin", "old b", "2026-01-01T00:00:00Z")
	env.put(t, "sub/same.txt", "same", "2026-02-01T00:00:00Z")
	env.put(t, "root.txt", "root", "2026-02-01T00:00:00Z")
	env.fake.Files["sub/a.txt"] = filestest.Stored("old a", "2026-01-01T00:00:00.000Z", "v1")
	env.fake.Files["sub/b.bin"] = filestest.Stored("new b", "2026-03-01T00:00:00.000Z", "v2")
	env.fake.Files["sub/same.txt"] = filestest.Stored("same", "2026-02-01T00:00:00.000Z", "v3")
	env.fake.Files["root.txt"] = filestest.Stored("root", "2026-02-01T00:00:00.000Z", "v4")
	// An unrelated root file the nested download must not replace.
	env.put(t, "b.bin", "root b", "2026-01-01T00:00:00Z")
	env.fake.Files["b.bin"] = filestest.Stored("root b", "2026-01-01T00:00:00.000Z", "v5")

	report := env.mustSync(t, env.local, "", syncOptions{})

	expect(t, "uploaded", report.Uploaded, 1)
	expect(t, "downloaded", report.Downloaded, 1)
	expect(t, "skipped", report.Skipped, 3)
	expect(t, "conflicts", report.Conflicts, 0)
	expect(t, "remote sub/a.txt", remoteText(env.fake.Files, "sub/a.txt"), "new a")
	expect(t, "local sub/b.bin", localText(filepath.Join(env.local, "sub/b.bin")), "new b")
	expect(t, "local b.bin", localText(filepath.Join(env.local, "b.bin")), "root b")
	expect(t, "local root.txt", localText(filepath.Join(env.local, "root.txt")), "root")
}

func TestSyncCallsASameTimeSizeChangeBelowTheRootAConflict(t *testing.T) {
	env := setUpSync(t)
	env.put(t, "sub/a.txt", "local copy", "2026-02-01T00:00:00Z")
	env.fake.Files["sub/a.txt"] = filestest.Stored("remote copy", "2026-02-01T00:00:00.000Z", "v1")

	report := env.mustSync(t, env.local, "", syncOptions{})

	expect(t, "conflicts", report.Conflicts, 1)
	expect(t, "transfers", report.Uploaded+report.Downloaded, 0)
	expect(t, "remote sub/a.txt", remoteText(env.fake.Files, "sub/a.txt"), "remote copy")
	expect(t, "local sub/a.txt", localText(filepath.Join(env.local, "sub/a.txt")), "local copy")
}

func TestSyncReportsANestedFileNameHeldByARemoteFolder(t *testing.T) {
	env := setUpSync(t)
	env.put(t, "sub/a.txt", "x", "2026-02-01T00:00:00Z")
	env.fake.Dirs["sub/a.txt"] = true

	report := env.mustSync(t, env.local, "", syncOptions{})

	expect(t, "conflicts", report.Conflicts, 1)
	expect(t, "transfers", report.Uploaded+report.Downloaded, 0)
	expect(t, "remote sub/a.txt stored", env.fake.Files["sub/a.txt"] != nil, false)
}

func TestSyncRefusesToReplaceANestedLocalSymlinkWithARemoteFile(t *testing.T) {
	env := setUpSync(t)
	env.put(t, "sub/target.txt", "x", "2026-02-01T00:00:00Z")
	symlink(t, "target.txt", filepath.Join(env.local, "sub/link.txt"))
	env.fake.Files["sub/link.txt"] = filestest.Stored("remote", "2026-03-01T00:00:00.000Z", "v1")

	report := env.mustSync(t, env.local, "", syncOptions{})

	expect(t, "conflicts", report.Conflicts, 1)
	expect(t, "downloaded", report.Downloaded, 0)
	target, err := os.Readlink(filepath.Join(env.local, "sub/link.txt"))

	if err != nil {
		t.Fatal(err)
	}

	expect(t, "link target", target, "target.txt")
}

func TestSyncSkipsFilesThatAlreadyMatchOnBothSides(t *testing.T) {
	env := setUpSync(t)
	env.put(t, "a.txt", "same", "2026-02-01T00:00:00Z")
	env.fake.Files["a.txt"] = filestest.Stored("same", "2026-02-01T00:00:00.000Z", "v1")

	report := env.mustSync(t, env.local, "", syncOptions{})

	expect(t, "skipped", report.Skipped, 1)
	expect(t, "transfers", report.Uploaded+report.Downloaded, 0)

	for _, request := range env.fake.Requests {
		if strings.HasSuffix(request, "/uploads") || strings.HasSuffix(request, "/content") {
			t.Errorf("request %q transferred a file that matched", request)
		}
	}
}

func TestSyncCallsASameTimeSizeChangeAConflictRatherThanAGuess(t *testing.T) {
	env := setUpSync(t)
	env.put(t, "a.txt", "local side", "2026-02-01T00:00:00Z")
	env.fake.Files["a.txt"] = filestest.Stored("remote side", "2026-02-01T00:00:00.000Z", "v1")

	report := env.mustSync(t, env.local, "", syncOptions{})

	expect(t, "conflicts", report.Conflicts, 1)
	expect(t, "transfers", report.Uploaded+report.Downloaded, 0)
	expect(t, "remote a.txt", remoteText(env.fake.Files, "a.txt"), "remote side")
	expect(t, "local a.txt", localText(filepath.Join(env.local, "a.txt")), "local side")
}

func TestSyncRefusesToReplaceARemoteFileThatChangedWhileSyncing(t *testing.T) {
	env := setUpSync(t)
	env.put(t, "a.txt", "new", "2026-03-02T00:00:00Z")
	env.fake.Files["a.txt"] = filestest.Stored("newer still", "2026-03-01T00:00:00.000Z", "v2")
	// The listing read a version the stored file has since moved past.
	env.fake.ListVersions["a.txt"] = "v1"

	report := env.mustSync(t, env.local, "", syncOptions{})

	expect(t, "conflicts", report.Conflicts, 1)
	expect(t, "uploaded", report.Uploaded, 0)
	expect(t, "remote a.txt", remoteText(env.fake.Files, "a.txt"), "newer still")
}

func TestSyncReplacesTheOlderSideWithForce(t *testing.T) {
	env := setUpSync(t)
	env.put(t, "a.txt", "old local", "2026-01-01T00:00:00Z")
	env.fake.Files["a.txt"] = filestest.Stored("new remote", "2026-02-01T00:00:00.000Z", "v1")

	report := env.mustSync(t, env.local, "", syncOptions{Mode: syncDownload, Force: true})

	expect(t, "downloaded", report.Downloaded, 1)
	expect(t, "local a.txt", localText(filepath.Join(env.local, "a.txt")), "new remote")
}

func TestSyncNeedsForceToNameASide(t *testing.T) {
	env := setUpSync(t)

	_, err := env.sync(env.local, "", syncOptions{Force: true})
	expectError(t, err, "--force needs")
}

func TestSyncNamesTheWinningSideOfASameTimeChangeWithForceIdenticalFilesStillSkip(t *testing.T) {
	env := setUpSync(t)
	env.put(t, "same-time.txt", "local", "2026-02-01T00:00:00Z")
	env.put(t, "identical.txt", "same", "2026-02-01T00:00:00Z")
	env.fake.Files["same-time.txt"] = filestest.Stored("remote", "2026-02-01T00:00:00.000Z", "v1")
	env.fake.Files["identical.txt"] = filestest.Stored("same", "2026-02-01T00:00:00.000Z", "v2")

	report := env.mustSync(t, env.local, "", syncOptions{Mode: syncUpload, Force: true})

	expect(t, "uploaded", report.Uploaded, 1)
	expect(t, "skipped", report.Skipped, 1)
	expect(t, "remote same-time.txt", remoteText(env.fake.Files, "same-time.txt"), "local")
}

func TestSyncNamesASideWithForceWhenTheRemoteFileHasNoTimestamp(t *testing.T) {
	env := setUpSync(t)
	env.put(t, "a.txt", "local", "2026-02-01T00:00:00Z")
	env.fake.Files["a.txt"] = filestest.Stored("remote", "not a time", "v1")

	report := env.mustSync(t, env.local, "", syncOptions{Mode: syncUpload, Force: true})

	expect(t, "uploaded", report.Uploaded, 1)
	expect(t, "remote a.txt", remoteText(env.fake.Files, "a.txt"), "local")
}

func TestSyncCreatesMissingRemoteFoldersWithMkdirsEmptyOnesToo(t *testing.T) {
	env := setUpSync(t)
	env.put(t, "sub/a.txt", "x", "2026-02-01T00:00:00Z")

	if err := os.Mkdir(filepath.Join(env.local, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}

	report := env.mustSync(t, env.local, "models", syncOptions{Mkdirs: true})

	expect(t, "uploaded", report.Uploaded, 1)
	expect(t, "directories", report.Directories, 3)
	expect(t, "remote models/sub/a.txt stored", env.fake.Files["models/sub/a.txt"] != nil, true)
	expect(t, "remote models", env.fake.Dirs["models"], true)
	expect(t, "remote models/sub", env.fake.Dirs["models/sub"], true)
	expect(t, "remote models/empty", env.fake.Dirs["models/empty"], true)
}

func TestSyncRefusesAMissingRemoteFolderWithoutMkdirs(t *testing.T) {
	env := setUpSync(t)
	env.put(t, "a.txt", "x", "2026-02-01T00:00:00Z")

	_, err := env.sync(env.local, "models", syncOptions{})
	expectError(t, err, "does not exist")
}

func TestSyncCreatesTheLocalFolderForDownloadsWithMkdirs(t *testing.T) {
	env := setUpSync(t)
	env.fake.Files["models/a.txt"] = filestest.Stored("a", "2026-02-01T00:00:00.000Z", "v1")
	into := filepath.Join(env.dir, "out")

	report := env.mustSync(t, into, "models", syncOptions{Mode: syncDownload, Mkdirs: true})

	expect(t, "downloaded", report.Downloaded, 1)
	expect(t, "directories", report.Directories, 1)
	expect(t, "out/a.txt", localText(filepath.Join(into, "a.txt")), "a")
}

func TestSyncRefusesAMissingLocalFolderWithoutMkdirs(t *testing.T) {
	env := setUpSync(t)
	env.fake.Files["models/a.txt"] = filestest.Stored("a", "2026-02-01T00:00:00.000Z", "v1")

	_, err := env.sync(filepath.Join(env.dir, "out"), "models", syncOptions{Mode: syncDownload})
	expectError(t, err, "--mkdirs")
}

func TestSyncSendsOneWayOnlyLeavingTheOtherSideAlone(t *testing.T) {
	env := setUpSync(t)
	env.put(t, "a.txt", "local only", "2026-02-01T00:00:00Z")
	env.fake.Files["b.bin"] = filestest.Stored("remote only", "2026-02-01T00:00:00.000Z", "v1")

	report := env.mustSync(t, env.local, "", syncOptions{Mode: syncDownload})

	expect(t, "downloaded", report.Downloaded, 1)
	expect(t, "uploaded", report.Uploaded, 0)
	expect(t, "remote a.txt stored", env.fake.Files["a.txt"] != nil, false)
	expect(t, "local a.txt", localText(filepath.Join(env.local, "a.txt")), "local only")
	expect(t, "local b.bin", localText(filepath.Join(env.local, "b.bin")), "remote only")
}

func TestSyncOnlySyncsPathsTheIncludeAndExcludeGlobsLetThrough(t *testing.T) {
	env := setUpSync(t)
	env.put(t, "models/a.bin", "x", "2026-02-01T00:00:00Z")
	env.put(t, "models/notes.txt", "x", "2026-02-01T00:00:00Z")
	env.put(t, "other/c.bin", "x", "2026-02-01T00:00:00Z")

	options := syncOptions{Include: []string{"models/**"}, Exclude: []string{"**/*.txt"}, Mkdirs: true}
	report := env.mustSync(t, env.local, "", options)

	expect(t, "uploaded", report.Uploaded, 1)
	expect(t, "remote models/a.bin stored", env.fake.Files["models/a.bin"] != nil, true)
	expect(t, "remote models/notes.txt stored", env.fake.Files["models/notes.txt"] != nil, false)
	expect(t, "remote other/c.bin stored", env.fake.Files["other/c.bin"] != nil, false)
}

func TestSyncSkipsAnExcludedFolderWholeNotJustItsFiles(t *testing.T) {
	env := setUpSync(t)
	env.put(t, "node_modules/pkg/a.bin", "x", "2026-02-01T00:00:00Z")
	env.put(t, "keep.bin", "y", "2026-02-01T00:00:00Z")

	report := env.mustSync(t, env.local, "remote", syncOptions{Exclude: []string{"node_modules/**"}, Mkdirs: true})

	expect(t, "uploaded", report.Uploaded, 1)
	expect(t, "conflicts", report.Conflicts, 0)
	expect(t, "remote remote/keep.bin stored", env.fake.Files["remote/keep.bin"] != nil, true)
	expect(t, "remote remote/node_modules", env.fake.Dirs["remote/node_modules"], false)
}

func TestSyncDoesNotPruneAFolderOnAChildrenOnlyGlob(t *testing.T) {
	env := setUpSync(t)
	env.put(t, "logs/today/a.log", "x", "2026-02-01T00:00:00Z")
	env.put(t, "logs/keep.txt", "x", "2026-02-01T00:00:00Z")
	env.put(t, "logs/sub/keep.txt", "x", "2026-02-01T00:00:00Z")

	report := env.mustSync(t, env.local, "remote", syncOptions{Exclude: []string{"logs/*"}, Mkdirs: true})

	expect(t, "uploaded", report.Uploaded, 2)
	expect(t, "remote remote/logs/sub/keep.txt stored", env.fake.Files["remote/logs/sub/keep.txt"] != nil, true)
	expect(t, "remote remote/logs/today/a.log stored", env.fake.Files["remote/logs/today/a.log"] != nil, true)
	expect(t, "remote remote/logs/keep.txt stored", env.fake.Files["remote/logs/keep.txt"] != nil, false)
}

func TestSyncDoesNotCreateFoldersAnIncludeGlobCannotReach(t *testing.T) {
	env := setUpSync(t)
	env.put(t, "models/a.bin", "x", "2026-02-01T00:00:00Z")
	env.put(t, "other/b.bin", "y", "2026-02-01T00:00:00Z")
	env.put(t, "top.txt", "z", "2026-02-01T00:00:00Z")

	report := env.mustSync(t, env.local, "remote", syncOptions{Include: []string{"models/**"}, Mkdirs: true})

	expect(t, "uploaded", report.Uploaded, 1)
	expect(t, "remote remote/models/a.bin stored", env.fake.Files["remote/models/a.bin"] != nil, true)
	expect(t, "remote remote/models", env.fake.Dirs["remote/models"], true)
	expect(t, "remote remote/other", env.fake.Dirs["remote/other"], false)
}

func TestSyncLeavesRemoteStagingFileNamesOutOfTheSync(t *testing.T) {
	env := setUpSync(t)
	env.put(t, "a.txt", "x", "2026-02-01T00:00:00Z")
	env.fake.Files["b.bin.abcdef123456.partial"] = filestest.Stored("half", "2026-03-01T00:00:00.000Z", "v1")

	report := env.mustSync(t, env.local, "", syncOptions{})

	expect(t, "uploaded", report.Uploaded, 1)
	expect(t, "skipped", report.Skipped, 1)
	expect(t, "conflicts", report.Conflicts, 0)
	expect(t, "local staging file", localExists(filepath.Join(env.local, "b.bin.abcdef123456.partial")), false)
}

func TestSyncListsFoldersAheadOfTheWalkSeveralAtOnce(t *testing.T) {
	env := setUpSync(t)

	for index := range 12 {
		name := fmt.Sprintf("part-%d/a.txt", index)
		env.put(t, name, "same", "2026-02-01T00:00:00Z")
		env.fake.Files[name] = filestest.Stored("same", "2026-02-01T00:00:00.000Z", fmt.Sprintf("v%d", index))
	}

	env.fake.ListDelay = 20 * time.Millisecond

	report := env.mustSync(t, env.local, "", syncOptions{})

	expect(t, "skipped", report.Skipped, 12)

	if env.fake.MaxListingsInFlight <= 1 {
		t.Errorf("listings at once = %d, want more than 1", env.fake.MaxListingsInFlight)
	}

	// Listings stay under the file server's limit of 32 a second at prod's round trip.
	if env.fake.MaxListingsInFlight > 6 {
		t.Errorf("listings at once = %d, want at most 6", env.fake.MaxListingsInFlight)
	}
}

func TestSyncLeavesAnotherClientsUnfinishedUploadOutOfTheSync(t *testing.T) {
	env := setUpSync(t)
	// The file server stages an upload as <name>.<first 8 hex of its id>.partial until it finishes.
	env.fake.Files["a.txt"] = filestest.Stored("a", "2026-03-01T00:00:00.000Z", "v1")
	env.fake.Files["b.bin.96f44e9f.partial"] = filestest.Stored("half", "2026-03-01T00:00:00.000Z", "v2")

	report := env.mustSync(t, env.local, "", syncOptions{Mkdirs: true, Mode: syncDownload})

	expect(t, "downloaded", report.Downloaded, 1)
	expect(t, "local staging file", localExists(filepath.Join(env.local, "b.bin.96f44e9f.partial")), false)
}

func TestSyncShowsWhatItWouldDoWithoutWriting(t *testing.T) {
	env := setUpSync(t)
	env.put(t, "a.txt", "new", "2026-02-01T00:00:00Z")
	env.fake.Files["a.txt"] = filestest.Stored("old", "2026-01-01T00:00:00.000Z", "v1")

	report := env.mustSync(t, env.local, "", syncOptions{DryRun: true})

	expect(t, "planned", report.Planned, 1)
	expect(t, "uploaded", report.Uploaded, 0)
	expect(t, "remote a.txt", remoteText(env.fake.Files, "a.txt"), "old")

	if want := []syncAction{{Path: "a.txt", Action: actionWouldUpload}}; !slices.Equal(report.Actions, want) {
		t.Errorf("actions = %+v, want %+v", report.Actions, want)
	}
}

func TestSyncReportsAFolderNameHeldByARemoteFileDeletingNothing(t *testing.T) {
	env := setUpSync(t)
	env.put(t, "sub/a.txt", "x", "2026-02-01T00:00:00Z")
	env.fake.Files["sub"] = filestest.Stored("a file named sub", "2026-01-01T00:00:00.000Z", "v1")

	report := env.mustSync(t, env.local, "", syncOptions{})

	expect(t, "conflicts", report.Conflicts, 1)
	expect(t, "remote sub/a.txt stored", env.fake.Files["sub/a.txt"] != nil, false)
	expect(t, "remote sub", remoteText(env.fake.Files, "sub"), "a file named sub")
}

func TestSyncLeavesInterruptedDownloadStagingFilesOutOfTheSync(t *testing.T) {
	env := setUpSync(t)
	env.put(t, "a.txt.abcdef123456.partial", "half a file", "2026-02-01T00:00:00Z")

	report := env.mustSync(t, env.local, "", syncOptions{})

	expect(t, "uploaded", report.Uploaded, 0)
	expect(t, "skipped", report.Skipped, 0)
	expect(t, "files in the volume", len(env.fake.Files), 0)
}

func TestSyncSkipsLocalSymlinksAndSpecialFiles(t *testing.T) {
	env := setUpSync(t)
	env.put(t, "a.txt", "x", "2026-02-01T00:00:00Z")
	symlink(t, "a.txt", filepath.Join(env.local, "link.txt"))

	report := env.mustSync(t, env.local, "", syncOptions{})

	expect(t, "uploaded", report.Uploaded, 1)
	expect(t, "skipped", report.Skipped, 1)

	isSkipped := slices.ContainsFunc(report.Actions, func(action syncAction) bool {
		return action.Reason == "A local symlink or special file is not synchronized."
	})

	expect(t, "symlink skipped", isSkipped, true)
}

func TestSyncCallsAFileChangedOnBothSidesSinceTheLastSyncAConflictWhicheverIsNewer(t *testing.T) {
	env := setUpSync(t)
	env.put(t, "a.txt", "first", "2026-01-01T00:00:00Z")
	env.mustSync(t, env.local, "", syncOptions{})
	env.fake.Files["a.txt"] = filestest.Stored("remote edit", "2026-02-01T00:00:00.000Z", "v9")
	env.put(t, "a.txt", "local edit", "2026-03-01T00:00:00Z")

	report := env.mustSync(t, env.local, "", syncOptions{})

	expect(t, "conflicts", report.Conflicts, 1)
	expect(t, "transfers", report.Uploaded+report.Downloaded, 0)
	expect(t, "remote a.txt", remoteText(env.fake.Files, "a.txt"), "remote edit")
	expect(t, "local a.txt", localText(filepath.Join(env.local, "a.txt")), "local edit")
}

func TestSyncSendsAChangeMadeSinceTheLastSyncEvenWhenItsClockIsBehindTheOtherSide(t *testing.T) {
	env := setUpSync(t)
	env.put(t, "a.txt", "first", "2026-02-01T00:00:00Z")
	env.mustSync(t, env.local, "", syncOptions{})
	// Edited on a computer whose clock runs a day behind.
	env.put(t, "a.txt", "local edit", "2026-01-31T00:00:00Z")

	report := env.mustSync(t, env.local, "", syncOptions{})

	expect(t, "uploaded", report.Uploaded, 1)
	expect(t, "downloaded", report.Downloaded, 0)
	expect(t, "remote a.txt", remoteText(env.fake.Files, "a.txt"), "local edit")
}

func TestSyncLeavesAFileDeletedOnOneSideSinceTheLastSyncDeletedKeepingTheOtherCopy(t *testing.T) {
	env := setUpSync(t)
	env.put(t, "local-gone.txt", "x", "2026-02-01T00:00:00Z")
	env.put(t, "remote-gone.txt", "y", "2026-02-01T00:00:00Z")
	env.mustSync(t, env.local, "", syncOptions{})

	if err := os.Remove(filepath.Join(env.local, "local-gone.txt")); err != nil {
		t.Fatal(err)
	}

	delete(env.fake.Files, "remote-gone.txt")

	report := env.mustSync(t, env.local, "", syncOptions{})

	expect(t, "transfers", report.Uploaded+report.Downloaded, 0)
	expect(t, "conflicts", report.Conflicts, 0)
	expect(t, "local local-gone.txt", localExists(filepath.Join(env.local, "local-gone.txt")), false)
	expect(t, "remote remote-gone.txt stored", env.fake.Files["remote-gone.txt"] != nil, false)
	expect(t, "remote local-gone.txt stored", env.fake.Files["local-gone.txt"] != nil, true)
	expect(t, "local remote-gone.txt", localText(filepath.Join(env.local, "remote-gone.txt")), "y")
}

func TestSyncCallsAFileDeletedOnOneSideAndChangedOnTheOtherSinceTheLastSyncAConflict(t *testing.T) {
	env := setUpSync(t)
	env.put(t, "a.txt", "x", "2026-02-01T00:00:00Z")
	env.mustSync(t, env.local, "", syncOptions{})

	if err := os.Remove(filepath.Join(env.local, "a.txt")); err != nil {
		t.Fatal(err)
	}

	env.fake.Files["a.txt"] = filestest.Stored("remote edit", "2026-03-01T00:00:00.000Z", "v9")

	report := env.mustSync(t, env.local, "", syncOptions{})

	expect(t, "conflicts", report.Conflicts, 1)
	expect(t, "downloaded", report.Downloaded, 0)
	expect(t, "local a.txt", localExists(filepath.Join(env.local, "a.txt")), false)
}

func TestSyncDoesNotRecreateAFolderDeletedOnOneSideSinceTheLastSync(t *testing.T) {
	env := setUpSync(t)
	env.put(t, "sub/a.txt", "x", "2026-02-01T00:00:00Z")
	env.mustSync(t, env.local, "remote", syncOptions{Mkdirs: true})

	if err := os.RemoveAll(filepath.Join(env.local, "sub")); err != nil {
		t.Fatal(err)
	}

	report := env.mustSync(t, env.local, "remote", syncOptions{Mkdirs: true})

	expect(t, "directories", report.Directories, 0)
	expect(t, "downloaded", report.Downloaded, 0)
	expect(t, "local sub", localExists(filepath.Join(env.local, "sub")), false)
}

func TestSyncMatchesAPatternWithoutASlashAtAnyDepthSkippingAFolderItNamesWhole(t *testing.T) {
	env := setUpSync(t)
	env.put(t, "app.log", "x", "2026-02-01T00:00:00Z")
	env.put(t, "logs/app.log", "x", "2026-02-01T00:00:00Z")
	env.put(t, "node_modules/x.js", "x", "2026-02-01T00:00:00Z")
	env.put(t, "web/node_modules/react/index.js", "x", "2026-02-01T00:00:00Z")
	env.put(t, "src/main.ts", "x", "2026-02-01T00:00:00Z")

	report := env.mustSync(t, env.local, "remote", syncOptions{Exclude: []string{"node_modules", "*.log"}, Mkdirs: true})

	expect(t, "uploaded", report.Uploaded, 1)
	expect(t, "remote remote/src/main.ts stored", env.fake.Files["remote/src/main.ts"] != nil, true)
	expect(t, "remote remote/node_modules", env.fake.Dirs["remote/node_modules"], false)
	expect(t, "remote remote/web/node_modules", env.fake.Dirs["remote/web/node_modules"], false)
}

func TestSyncSkipsAFolderADoubleStarPatternNamesAtTheRootAndBelow(t *testing.T) {
	env := setUpSync(t)
	env.put(t, "node_modules/x.js", "x", "2026-02-01T00:00:00Z")
	env.put(t, "web/node_modules/react/index.js", "x", "2026-02-01T00:00:00Z")
	env.put(t, "web/app.js", "x", "2026-02-01T00:00:00Z")

	report := env.mustSync(t, env.local, "remote", syncOptions{Exclude: []string{"**/node_modules"}, Mkdirs: true})

	expect(t, "uploaded", report.Uploaded, 1)
	expect(t, "remote remote/web/app.js stored", env.fake.Files["remote/web/app.js"] != nil, true)
	expect(t, "remote remote/node_modules", env.fake.Dirs["remote/node_modules"], false)
	expect(t, "remote remote/web/node_modules", env.fake.Dirs["remote/web/node_modules"], false)
}

func TestSyncSyncsATreeOfMoreThan100000Entries(t *testing.T) {
	env := setUpSync(t)
	many := filepath.Join(env.local, "many")

	if err := os.MkdirAll(many, 0o755); err != nil {
		t.Fatal(err)
	}

	for index := range 100_001 {
		if err := os.WriteFile(filepath.Join(many, fmt.Sprintf("%d.bin", index)), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	report := env.mustSync(t, env.local, "", syncOptions{Mkdirs: true, DryRun: true})

	expect(t, "planned", report.Planned, 100_001)
}

func TestSyncCallsFilesAMillisecondApartDifferentAndWithinOneMillisecondTheSameAsCpUpdateDoes(t *testing.T) {
	env := setUpSync(t)
	env.fake.Files["apart.txt"] = filestest.Stored("same", "2026-02-01T00:00:00.000Z", "v1")
	env.fake.Files["within.txt"] = filestest.Stored("same", "2026-02-01T00:00:00.000Z", "v2")
	env.put(t, "apart.txt", "same", "2026-02-01T00:00:00Z")
	env.put(t, "within.txt", "same", "2026-02-01T00:00:00Z")
	// The local clock keeps time below a millisecond; the volume keeps whole milliseconds.
	base := time.Date(2026, time.February, 1, 0, 0, 0, 0, time.UTC)
	apart := base.Add(1500 * time.Microsecond)
	within := base.Add(400 * time.Microsecond)

	if err := os.Chtimes(filepath.Join(env.local, "apart.txt"), apart, apart); err != nil {
		t.Fatal(err)
	}

	if err := os.Chtimes(filepath.Join(env.local, "within.txt"), within, within); err != nil {
		t.Fatal(err)
	}

	report := env.mustSync(t, env.local, "", syncOptions{})

	expect(t, "uploaded", report.Uploaded, 1)
	expect(t, "skipped", report.Skipped, 1)

	if !slices.Contains(report.Actions, syncAction{Path: "apart.txt", Action: actionUploaded}) {
		t.Errorf("actions = %+v, want apart.txt uploaded", report.Actions)
	}
}
