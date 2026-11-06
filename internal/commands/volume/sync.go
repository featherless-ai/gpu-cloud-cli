package volume

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"featherless.ai/featherless-cloud/internal/commands/shared"
	"featherless.ai/featherless-cloud/internal/completion"
	"featherless.ai/featherless-cloud/internal/config"
	"featherless.ai/featherless-cloud/internal/errs"
	"featherless.ai/featherless-cloud/internal/files"
	"featherless.ai/featherless-cloud/internal/output"
)

const (
	maxDepth         = 20
	maxReportActions = 1000
	// Listings run ahead of the walk, a few at once, so it doesn't wait a round trip per folder. Six
	// at prod's ~0.2 s round trip stays under the file server's 32 listings a second.
	listingsAhead = 6
)

var (
	// This CLI's interrupted download staging files, `name.<12 hex>.partial`.
	partialPattern = regexp.MustCompile(`\.[0-9a-f]{12}\.partial$`)
	// Staging files in a volume: the CLI's, and the file server's unfinished uploads, `name.<8 hex>.partial`.
	remoteStagingPattern = regexp.MustCompile(`\.(?:[0-9a-f]{8}|[0-9a-f]{12})\.partial$`)
	globSpecials         = regexp.MustCompile(`[|\\{}()[\]^$+?.]`)
	wildcard             = regexp.MustCompile(`[*?]`)
)

type syncMode string

const (
	syncUpload   syncMode = "upload"
	syncDownload syncMode = "download"
	syncBoth     syncMode = "both"
)

type syncOptions struct {
	// Mode is both when empty.
	Mode   syncMode
	DryRun bool
	Mkdirs bool
	Force  bool
	// Concurrency is the default when 0.
	Concurrency int
	Include     []string
	Exclude     []string
	// Verbose prints each file as it syncs.
	Verbose bool
	// Progress shows a running total on stderr.
	Progress bool
}

type syncActionKind string

const (
	actionUploaded             syncActionKind = "uploaded"
	actionDownloaded           syncActionKind = "downloaded"
	actionWouldUpload          syncActionKind = "would-upload"
	actionWouldDownload        syncActionKind = "would-download"
	actionSkipped              syncActionKind = "skipped"
	actionConflict             syncActionKind = "conflict"
	actionCreatedDirectory     syncActionKind = "created-directory"
	actionWouldCreateDirectory syncActionKind = "would-create-directory"
)

type syncAction struct {
	Path   string         `json:"path"`
	Action syncActionKind `json:"action"`
	Reason string         `json:"reason,omitempty"`
}

type syncFailure struct {
	Path  string `json:"path"`
	Error string `json:"error"`
}

type syncReport struct {
	Uploaded        int           `json:"uploaded"`
	UploadedBytes   int64         `json:"uploadedBytes"`
	Downloaded      int           `json:"downloaded"`
	DownloadedBytes int64         `json:"downloadedBytes"`
	Planned         int           `json:"planned"`
	Skipped         int           `json:"skipped"`
	Conflicts       int           `json:"conflicts"`
	Directories     int           `json:"directories"`
	Failed          []syncFailure `json:"failed"`
	Actions         []syncAction  `json:"actions"`
	Truncated       bool          `json:"truncated"`
}

// glob turns a pattern into a regular expression over a path below the sync
// root: `*` any run but `/`, `**` any run, `?` one character but `/`. An
// unanchored pattern matches a name at any depth, as in .gitignore; patterns
// with a slash are anchored at the root.
func glob(pattern string, anchored bool) (*regexp.Regexp, error) {
	if pattern == "" || utf8.RuneCountInString(pattern) > 256 {
		return nil, errs.New("Glob patterns must contain 1–256 characters.")
	}

	var text strings.Builder

	if anchored {
		text.WriteString("^")
	} else {
		text.WriteString("^(?:.*/)?")
	}

	for index := 0; index < len(pattern); index++ {
		char := pattern[index]
		isDoubleStar := char == '*' && index+1 < len(pattern) && pattern[index+1] == '*'

		switch {
		case isDoubleStar && index+2 < len(pattern) && pattern[index+2] == '/':
			text.WriteString("(?:.*/)?")
			index += 2
		case isDoubleStar:
			text.WriteString(".*")
			index++
		case char == '*':
			text.WriteString("[^/]*")
		case char == '?':
			text.WriteString("[^/]")
		default:
			text.WriteString(globSpecials.ReplaceAllString(string(char), `\$0`))
		}
	}

	text.WriteString("$")

	return regexp.Compile(text.String())
}

// globAt is glob anchored as the pattern reads: with a slash it matches from the root.
func globAt(pattern string) (*regexp.Regexp, error) {
	return glob(pattern, strings.Contains(pattern, "/"))
}

// synced is a file as the last sync left it on both sides: its size and
// whole-millisecond modification time. NaN stands where JSON holds null.
type synced struct {
	Size       float64
	ModifiedAt float64
}

type savedSync struct {
	Size       *float64 `json:"size"`
	ModifiedAt *float64 `json:"modifiedAt"`
}

func numberOrNaN(value *float64) float64 {
	if value == nil {
		return math.NaN()
	}

	return *value
}

func numberOrNull(value float64) *float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return nil
	}

	return &value
}

type taskKind int

const (
	taskUpload taskKind = iota
	taskDownload
	taskMkdirRemote
	taskMkdirLocal
)

// syncTask is one planned step of a sync: a file one way or the other, or a
// folder to create. key is the path below the root.
type syncTask struct {
	kind       taskKind
	key        string
	local      string
	remote     string
	entry      files.FileEntry
	bytes      int64
	modifiedAt float64
	replace    bool
	ifMatch    string
	// expected is the local file a download replaces, as the listing read it.
	expected *localEntry
}

func (t *syncTask) isTransfer() bool {
	return t.kind == taskUpload || t.kind == taskDownload
}

// walkMode is how a folder pairs up below the sync root: on both sides, or only one.
type walkMode int

const (
	walkPair walkMode = iota
	walkLocal
	walkRemote
)

type localEntry struct {
	full    string
	size    int64
	mtimeMs float64
	info    os.FileInfo
}

// localListing is the local side of one folder: files to diff, folders to
// walk, and names nothing can replace.
type localListing struct {
	files   map[string]localEntry
	dirs    map[string]bool
	special map[string]bool
}

type folderListing struct {
	local  *localListing
	remote []files.FileEntry
}

type listingFuture struct {
	done    chan struct{}
	listing folderListing
	err     error
}

type syncRun struct {
	ctx        context.Context
	out        *output.Context
	data       volumeFiles
	options    syncOptions
	mode       syncMode
	root       string
	remoteBase string
	isJSON     bool

	history         map[string]synced
	historyFolders  map[string]bool
	historyChildren map[string][]string
	include         []*regexp.Regexp
	exclude         []*regexp.Regexp
	folderExcludes  []*regexp.Regexp

	// mu guards what the walk and the transfers share: the report, what is
	// synced, and the progress shown.
	mu         sync.Mutex
	report     syncReport
	synced     map[string]synced
	display    *progressDisplay
	total      count
	doneFiles  int
	moved      map[*syncTask]int64
	isDeclared bool

	listCtx    context.Context
	listingsMu sync.Mutex
	listings   map[string]*listingFuture
	slots      chan struct{}
}

// syncVolumeFolder syncs a local folder with a folder in a volume, folder by folder.
//
// Each folder is listed on both sides and diffed locally — one bounded, paged
// request per remote folder — so an interrupted sync resumes where the next
// one lists, without walking the whole volume again. Sync remembers what it
// left the same on both sides, so the side that changed since then wins
// whatever the clocks say, a file changed on both sides is a conflict, and a
// file deleted on one side stays deleted. For a file with no such memory the
// newer modification time wins, and same-millisecond copies of different
// sizes are a conflict rather than a guess. Sync never deletes: it only
// sends, receives and creates folders. Every remote write is version-guarded,
// so a file that changed remotely after the listing that planned its upload
// fails with a conflict instead of replacing it.
func syncVolumeFolder(ctx context.Context, out *output.Context, data volumeFiles, local, remoteBase string, options syncOptions) (syncReport, error) {
	mode := options.Mode

	if mode == "" {
		mode = syncBoth
	}

	if !slices.Contains([]syncMode{syncUpload, syncDownload, syncBoth}, mode) {
		return syncReport{}, errs.New("Sync mode must be upload, download or both.")
	}

	if options.Force && mode == syncBoth {
		return syncReport{}, errs.New("--force needs --mode upload or --mode download; name which side wins.")
	}

	limit := options.Concurrency

	if limit == 0 {
		limit = defaultConcurrency
	}

	if limit < 1 || limit > maxConcurrency {
		return syncReport{}, errs.Newf("--concurrency must be 1 to %d.", maxConcurrency)
	}

	if remoteBase != "" {
		if _, err := remotePath(remoteBase); err != nil {
			return syncReport{}, err
		}
	}

	given, err := filepath.Abs(local)

	if err != nil {
		return syncReport{}, err
	}

	rootInfo, rootErr := os.Lstat(given)
	hasRoot := rootErr == nil

	if hasRoot && (!rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0) {
		return syncReport{}, errs.New(local + " is not a real folder; sync the folder itself, not a link to it.")
	}

	// Links above the folder are fine, like macOS's /tmp; its real path names it the same way every time.
	root := given

	if hasRoot {
		root, err = filepath.EvalSymlinks(given)

		if err != nil {
			return syncReport{}, err
		}
	}

	if !hasRoot && (mode == syncUpload || !options.Mkdirs) {
		return syncReport{}, errs.New(local + " does not exist; pass --mkdirs to create it for downloads.")
	}

	if !hasRoot {
		parent := filepath.Dir(root)
		parentInfo, err := os.Lstat(parent)

		if err != nil || !parentInfo.IsDir() || parent == root {
			return syncReport{}, errs.New("The parent of " + local + " does not exist; sync creates only the folder itself.")
		}
	}

	var remoteRoot *files.FileEntry

	if remoteBase != "" {
		remoteRoot, err = data.Files.Entry(ctx, remoteBase)

		if err != nil {
			return syncReport{}, err
		}
	}

	isRemoteMissing := remoteBase != "" && remoteRoot == nil

	if remoteRoot != nil && remoteRoot.Type == files.TypeFile {
		return syncReport{}, errs.New(shown(data.VolumeName, remoteBase) + " is a file, not a folder.")
	}

	if isRemoteMissing && (mode == syncDownload || !options.Mkdirs) {
		return syncReport{}, errs.New(shown(data.VolumeName, remoteBase) + " does not exist; pass --mkdirs to create it for uploads.")
	}

	// What the last sync of this pair of folders left the same on both sides, by path below the
	// root. A side missing whole is a fresh copy, not a deletion of everything, so it starts over.
	pair := sha256.Sum256([]byte(data.Files.VolumeID() + "\n" + remoteBase + "\n" + root))
	historyFile := filepath.Join(config.Dir(), "featherless-cloud-sync", hex.EncodeToString(pair[:])[:32]+".json")
	history := map[string]synced{}

	if hasRoot && !isRemoteMissing {
		history = readHistory(historyFile)
	}

	listCtx, stopListings := context.WithCancel(ctx)
	defer stopListings()

	run := &syncRun{
		ctx: ctx, out: out, data: data, options: options, mode: mode, root: root, remoteBase: remoteBase, isJSON: out.Mode == output.JSON,
		history: history, historyFolders: map[string]bool{}, historyChildren: map[string][]string{},
		report: syncReport{Failed: []syncFailure{}, Actions: []syncAction{}},
		synced: map[string]synced{}, moved: map[*syncTask]int64{},
		listCtx: listCtx, listings: map[string]*listingFuture{}, slots: make(chan struct{}, listingsAhead),
	}

	for key, value := range history {
		run.synced[key] = value
		parent := ""

		if slash := strings.LastIndex(key, "/"); slash != -1 {
			parent = key[:slash]
		}

		run.historyChildren[parent] = append(run.historyChildren[parent], key)

		for at := strings.Index(key, "/"); at != -1; at = indexFrom(key, "/", at+1) {
			run.historyFolders[key[:at]] = true
		}
	}

	if err := run.compileGlobs(); err != nil {
		return syncReport{}, err
	}

	run.display = newProgressDisplay(out, "Syncing", options.Progress && !run.isJSON)

	// The walk plans folder by folder while the workers transfer, so a deep tree
	// starts moving before the whole volume has been listed. A walk failure lets
	// the transfers drain, then surfaces.
	rootMode := walkPair

	if !hasRoot {
		rootMode = walkRemote
	} else if isRemoteMissing {
		rootMode = walkLocal
	}

	tasks := make(chan *syncTask)
	var planErr error

	go func() {
		defer close(tasks)

		planErr = run.walk("", rootMode, func(task *syncTask) error {
			select {
			case tasks <- task:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()

	var workers sync.WaitGroup

	for range limit {
		workers.Go(func() {
			for task := range tasks {
				run.perform(task)
			}
		})
	}

	workers.Wait()
	run.mu.Lock()
	run.display.finish()
	run.mu.Unlock()

	// Kept even when the walk failed: what did finish stays remembered for the next run.
	if !options.DryRun {
		if err := saveHistory(historyFile, run.synced); err != nil {
			return syncReport{}, err
		}
	}

	if planErr != nil {
		return syncReport{}, planErr
	}

	return run.report, nil
}

func indexFrom(text, part string, from int) int {
	at := strings.Index(text[from:], part)

	if at == -1 {
		return -1
	}

	return from + at
}

func readHistory(historyFile string) map[string]synced {
	history := map[string]synced{}
	content, err := os.ReadFile(historyFile)

	if err != nil {
		return history
	}

	var saved struct {
		Files map[string]savedSync `json:"files"`
	}

	if err := json.Unmarshal(content, &saved); err != nil {
		return history
	}

	for key, value := range saved.Files {
		history[key] = synced{Size: numberOrNaN(value.Size), ModifiedAt: numberOrNaN(value.ModifiedAt)}
	}

	return history
}

func saveHistory(historyFile string, files map[string]synced) error {
	saved := map[string]savedSync{}

	for key, value := range files {
		saved[key] = savedSync{Size: numberOrNull(value.Size), ModifiedAt: numberOrNull(value.ModifiedAt)}
	}

	var content bytes.Buffer
	encoder := json.NewEncoder(&content)
	encoder.SetEscapeHTML(false)

	if err := encoder.Encode(map[string]any{"files": saved}); err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(historyFile), 0o777); err != nil {
		return err
	}

	draft := historyFile + "." + strconv.Itoa(os.Getpid()) + ".tmp"

	if err := os.WriteFile(draft, bytes.TrimSuffix(content.Bytes(), []byte("\n")), 0o666); err != nil {
		return err
	}

	return os.Rename(draft, historyFile)
}

func (s *syncRun) compileGlobs() error {
	for _, pattern := range s.options.Include {
		compiled, err := globAt(pattern)

		if err != nil {
			return err
		}

		s.include = append(s.include, compiled)
	}

	for _, pattern := range s.options.Exclude {
		compiled, err := globAt(pattern)

		if err != nil {
			return err
		}

		s.exclude = append(s.exclude, compiled)
	}

	// A folder is skipped whole when a pattern names the folder itself:
	// `node_modules/**` covers everything below node_modules, and a last part
	// without wildcards — `node_modules`, `**/node_modules` — names the folder.
	// `logs/*` and `*.log` name files, so nested files below a folder they
	// match must still be reachable.
	for _, pattern := range s.options.Exclude {
		var compiled *regexp.Regexp
		var err error

		if folder, isBelow := strings.CutSuffix(pattern, "/**"); isBelow {
			if folder == "" {
				folder = "*"
			}

			compiled, err = glob(folder, true)
		} else if !wildcard.MatchString(lastPart(pattern)) {
			compiled, err = globAt(pattern)
		} else {
			continue
		}

		if err != nil {
			return err
		}

		s.folderExcludes = append(s.folderExcludes, compiled)
	}

	return nil
}

func (s *syncRun) target(relative string) string {
	return joinPath(s.remoteBase, relative)
}

func (s *syncRun) tooDeep(relative string) bool {
	return len(strings.Split(s.target(relative), "/")) > maxDepth
}

func matchesAny(patterns []*regexp.Regexp, relative string) bool {
	return slices.ContainsFunc(patterns, func(pattern *regexp.Regexp) bool { return pattern.MatchString(relative) })
}

func (s *syncRun) accepted(relative string) bool {
	isIncluded := len(s.include) == 0 || matchesAny(s.include, relative)
	return isIncluded && !matchesAny(s.exclude, relative)
}

func (s *syncRun) excluded(relative string) bool {
	return matchesAny(s.folderExcludes, relative)
}

// reachable is the other side of the globs: with --include, a folder nothing
// below can match is skipped too. A pattern can only reach below a folder when
// it matches at any depth, or its literal prefix can be a prefix of the paths
// below, or starts with a wildcard; anything else would need a path the glob
// cannot spell.
func (s *syncRun) reachable(folder string) bool {
	if !slices.ContainsFunc(s.options.Include, func(pattern string) bool { return pattern != "" }) {
		return true
	}

	return slices.ContainsFunc(s.options.Include, func(pattern string) bool {
		prefix := pattern

		if at := wildcard.FindStringIndex(pattern); at != nil {
			prefix = pattern[:at[0]]
		}

		below := folder + "/"

		return !strings.Contains(pattern, "/") || prefix == "" || strings.HasPrefix(below, prefix) || strings.HasPrefix(prefix, below)
	})
}

// record keeps an action for the report, up to a limit; callers hold mu.
func (s *syncRun) record(action syncAction) {
	if len(s.report.Actions) < maxReportActions {
		s.report.Actions = append(s.report.Actions, action)
	} else {
		s.report.Truncated = true
	}
}

// sayOut and sayErr print a line outside JSON mode; callers hold mu.
func (s *syncRun) sayOut(line string) {
	if !s.isJSON {
		s.display.say(s.out.Stdout, line)
	}
}

func (s *syncRun) sayErr(line string) {
	if !s.isJSON {
		s.display.say(s.out.Stderr, line)
	}
}

func (s *syncRun) conflict(path, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.report.Conflicts++
	s.record(syncAction{Path: path, Action: actionConflict, Reason: reason})
	s.sayErr("conflict " + path + ": " + reason)
}

func (s *syncRun) skip(path, reason string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.report.Skipped++
	s.record(syncAction{Path: path, Action: actionSkipped, Reason: reason})
}

func (s *syncRun) listLocal(folder, relative string) (*localListing, error) {
	listing := &localListing{files: map[string]localEntry{}, dirs: map[string]bool{}, special: map[string]bool{}}
	children, err := os.ReadDir(folder)

	if err != nil {
		return nil, err
	}

	for _, child := range children {
		// Children are keyed by their path below the sync root, the same shape
		// the remote side is keyed by, so the two sides diff below the root.
		name := joinPath(relative, child.Name())

		if child.Type()&os.ModeSymlink != 0 {
			listing.special[name] = true
			continue
		}

		full := filepath.Join(folder, child.Name())
		info, err := os.Lstat(full)

		if err != nil {
			return nil, err
		}

		switch {
		case info.IsDir():
			listing.dirs[name] = true
		case info.Mode().IsRegular():
			// An interrupted download's staging file is not a file to synchronize.
			if partialPattern.MatchString(child.Name()) || child.Name() == ".featherless-uploads" {
				continue
			}

			listing.files[name] = localEntry{full: full, size: info.Size(), mtimeMs: files.Milliseconds(info.ModTime()), info: info}
		default:
			listing.special[name] = true
		}
	}

	return listing, nil
}

// listRemote is the remote side of one folder, or nothing when the folder is not there.
func (s *syncRun) listRemote(folder string) ([]files.FileEntry, error) {
	entries, err := s.data.Files.List(s.listCtx, folder)
	var refused *files.FileAPIError

	if errors.As(err, &refused) && refused.Status == 404 {
		return []files.FileEntry{}, nil
	}

	return entries, err
}

// listFolder lists a folder on the sides that hold it, a few folders at once.
func (s *syncRun) listFolder(relative string, mode walkMode) *listingFuture {
	future := &listingFuture{done: make(chan struct{})}

	go func() {
		defer close(future.done)

		select {
		case s.slots <- struct{}{}:
		case <-s.listCtx.Done():
			future.err = s.listCtx.Err()
			return
		}

		defer func() { <-s.slots }()
		var group sync.WaitGroup
		var localErr, remoteErr error

		if mode != walkRemote {
			group.Go(func() {
				future.listing.local, localErr = s.listLocal(filepath.Join(s.root, relative), relative)
			})
		}

		if mode != walkLocal {
			group.Go(func() {
				future.listing.remote, remoteErr = s.listRemote(s.target(relative))
			})
		}

		group.Wait()

		if localErr != nil {
			future.err = localErr
		} else if remoteErr != nil {
			future.err = remoteErr
		}
	}()

	return future
}

func (s *syncRun) isSkippedFolder(relative string, mode walkMode) bool {
	return mode != walkPair && s.historyFolders[relative] && !s.options.Force
}

// listAhead starts listing a folder the walk will reach, where walking it lists it at all.
func (s *syncRun) listAhead(relative string, mode walkMode) {
	isRefused := (mode == walkLocal && (s.tooDeep(relative) || !s.options.Mkdirs)) || (mode == walkRemote && !s.options.Mkdirs)
	s.listingsMu.Lock()
	defer s.listingsMu.Unlock()

	if s.listings[relative] != nil || s.isSkippedFolder(relative, mode) || isRefused {
		return
	}

	s.listings[relative] = s.listFolder(relative, mode)
}

func (s *syncRun) takeListing(relative string, mode walkMode) (folderListing, error) {
	s.listingsMu.Lock()
	future := s.listings[relative]
	delete(s.listings, relative)
	s.listingsMu.Unlock()

	if future == nil {
		future = s.listFolder(relative, mode)
	}

	select {
	case <-future.done:
		return future.listing, future.err
	case <-s.ctx.Done():
		return folderListing{}, s.ctx.Err()
	}
}

// unchanged tells whether one side's copy is still the one the last sync left on both sides.
func unchanged(before synced, size, modifiedAt float64) bool {
	return size == before.Size && modifiedAt == before.ModifiedAt
}

func entrySize(entry files.FileEntry) float64 {
	if entry.SizeBytes == nil {
		return math.NaN()
	}

	return float64(*entry.SizeBytes)
}

func parsedTime(value string) float64 {
	milliseconds, ok := files.ParseTime(value)

	if !ok {
		return math.NaN()
	}

	return float64(milliseconds)
}

type child struct {
	name string
	mode walkMode
}

// walk walks one folder on whichever sides hold it, emitting the tasks it plans.
func (s *syncRun) walk(relative string, mode walkMode, emit func(*syncTask) error) error {
	// A folder the last sync left on both sides and one side has deleted since stays deleted.
	if s.isSkippedFolder(relative, mode) {
		reason := "Deleted in the volume since the last sync."

		if mode == walkRemote {
			reason = "Deleted on this computer since the last sync."
		}

		s.skip(s.target(relative), reason)

		return nil
	}

	switch mode {
	case walkLocal:
		if s.tooDeep(relative) {
			s.conflict(s.target(relative), "The volume cannot hold a path this deep.")
			return nil
		}

		if !s.options.Mkdirs {
			s.conflict(s.target(relative), "The remote folder is missing; pass --mkdirs to create it.")
			return nil
		}

		if err := emit(&syncTask{kind: taskMkdirRemote, remote: s.target(relative)}); err != nil {
			return err
		}
	case walkRemote:
		if !s.options.Mkdirs {
			s.conflict(s.target(relative), "The local folder is missing; pass --mkdirs to create it.")
			return nil
		}

		if err := emit(&syncTask{kind: taskMkdirLocal, local: filepath.Join(s.root, relative)}); err != nil {
			return err
		}
	}

	listing, err := s.takeListing(relative, mode)

	if err != nil {
		return err
	}

	local := listing.local

	if local == nil {
		local = &localListing{files: map[string]localEntry{}, dirs: map[string]bool{}, special: map[string]bool{}}
	}

	// Both sides are keyed by the path below the sync root, so they pair up
	// at every depth, not just at the root itself.
	remoteDirs := map[string]bool{}
	remoteFiles := map[string]files.FileEntry{}

	for _, entry := range listing.remote {
		if entry.Type == files.TypeDirectory {
			remoteDirs[joinPath(relative, entry.Name)] = true
		} else {
			remoteFiles[joinPath(relative, entry.Name)] = entry
		}
	}

	present := map[string]bool{}

	for _, names := range []map[string]bool{local.dirs, local.special, remoteDirs} {
		for name := range names {
			present[name] = true
		}
	}

	for name := range local.files {
		present[name] = true
	}

	for name := range remoteFiles {
		present[name] = true
	}

	names := make([]string, 0, len(present))

	for name := range present {
		names = append(names, name)
	}

	slices.Sort(names)

	// A file both sides have dropped since the last sync is forgotten.
	s.mu.Lock()

	for _, known := range s.historyChildren[relative] {
		if !present[known] {
			delete(s.synced, known)
		}
	}

	s.mu.Unlock()

	isPassedOver := func(name string) bool {
		isFolder := local.dirs[name] || remoteDirs[name]
		return (isFolder || local.special[name]) && (s.excluded(name) || !s.reachable(name))
	}

	// The folders the loop below descends into, in its order, as the loop decides them.
	childMode := func(name string) (walkMode, bool) {
		isDir := local.dirs[name]
		_, hasRemoteFile := remoteFiles[name]
		_, hasLocalFile := local.files[name]

		if local.special[name] || ((isDir || remoteDirs[name]) && (s.excluded(name) || !s.reachable(name))) {
			return walkPair, false
		}

		if isDir && remoteDirs[name] {
			return walkPair, true
		}

		if !isDir && !hasLocalFile && remoteDirs[name] {
			return walkRemote, s.mode != syncUpload
		}

		if isDir && !hasRemoteFile && s.mode != syncDownload {
			return walkLocal, true
		}

		return walkPair, false
	}

	var children []child

	for _, name := range names {
		if childWalk, isWalked := childMode(name); isWalked {
			children = append(children, child{name: name, mode: childWalk})
		}
	}

	ahead := 0
	entered := 0

	// Before entering a child folder, keep it and the next few listing in the background.
	enterChild := func() {
		for ahead < len(children) && ahead <= entered+listingsAhead {
			s.listAhead(children[ahead].name, children[ahead].mode)
			ahead++
		}

		entered++
	}

	for _, name := range names {
		file, hasLocalFile := local.files[name]
		isDir := local.dirs[name]
		entry, hasRemoteFile := remoteFiles[name]

		// A folder or a special name the globs skip is passed over whole, its
		// files with it; files themselves the diff below decides.
		if isPassedOver(name) {
			continue
		}

		// A local symlink or special file is never sent, and nothing remote
		// may take its name either.
		if local.special[name] {
			if remoteDirs[name] || hasRemoteFile {
				s.conflict(s.target(name), "A remote file or folder has this local symlink or special file name.")
			} else {
				s.skip(s.target(name), "A local symlink or special file is not synchronized.")
			}

			continue
		}

		// Folders on both sides.
		if isDir && remoteDirs[name] {
			enterChild()

			if err := s.walk(name, walkPair, emit); err != nil {
				return err
			}

			continue
		}

		// A name used as a folder on one side and a file on the other.
		if isDir && hasRemoteFile {
			s.conflict(s.target(name), "A remote file has this folder name.")
			continue
		}

		if !isDir && hasLocalFile && remoteDirs[name] {
			s.conflict(s.target(name), "A remote folder has this file name.")
			continue
		}

		// A folder only the remote holds: nothing local to send under it.
		if !isDir && !hasLocalFile && remoteDirs[name] {
			if s.mode != syncUpload {
				enterChild()

				if err := s.walk(name, walkRemote, emit); err != nil {
					return err
				}
			}

			continue
		}

		// A folder only the local side holds.
		if isDir && !hasRemoteFile && s.mode != syncDownload {
			enterChild()

			if err := s.walk(name, walkLocal, emit); err != nil {
				return err
			}

			continue
		}

		var task *syncTask

		switch {
		case hasLocalFile && hasRemoteFile:
			task = s.planBoth(name, file, entry)
		case hasLocalFile:
			task = s.planLocalOnly(name, file)
		case hasRemoteFile:
			task = s.planRemoteOnly(name, entry)
		}

		if task == nil {
			continue
		}

		if err := emit(task); err != nil {
			return err
		}
	}

	return nil
}

// planBoth decides a file both sides hold; nil when nothing moves.
func (s *syncRun) planBoth(name string, file localEntry, entry files.FileEntry) *syncTask {
	if !s.accepted(name) {
		return nil
	}

	remoteAt := parsedTime(entry.ModifiedAt)
	localAt := math.Floor(file.mtimeMs)
	size := float64(file.size)
	remoteSize := entrySize(entry)

	// Identical files have nothing to send either way, even with --force.
	if sameModifiedTime(file.mtimeMs, entry.ModifiedAt) && size == remoteSize {
		s.mu.Lock()
		s.synced[name] = synced{Size: size, ModifiedAt: remoteAt}
		s.report.Skipped++
		s.record(syncAction{Path: s.target(name), Action: actionSkipped})
		s.mu.Unlock()

		return nil
	}

	// Which sides changed since the last sync left them the same, when it did.
	before, isKnown := s.history[name]
	isLocalChanged := isKnown && !unchanged(before, size, localAt)
	isRemoteChanged := isKnown && !unchanged(before, remoteSize, remoteAt)
	var way syncMode

	switch {
	case s.options.Force:
		way = s.mode
	case math.IsNaN(remoteAt):
		s.conflict(s.target(name), "The remote file has no timestamp; pass --force to choose a side.")
		return nil
	case isLocalChanged && isRemoteChanged:
		s.conflict(s.target(name), "Both sides changed since the last sync; pass --force to choose one.")
		return nil
	case isLocalChanged != isRemoteChanged:
		// The side that changed since the last sync wins, whatever the clocks say.
		way = syncDownload

		if isLocalChanged {
			way = syncUpload
		}
	case localAt == remoteAt:
		s.conflict(s.target(name), "Both sides changed at the same time with different sizes; pass --force to choose one.")
		return nil
	case localAt > remoteAt:
		way = syncUpload
	default:
		way = syncDownload
	}

	if !s.options.Force && s.mode != syncBoth && way != s.mode {
		if size != remoteSize {
			side := "remote"

			if way == syncUpload {
				side = "local"
			}

			s.conflict(s.target(name), "The "+side+" copy has the newer change, with a different size; pass --force to replace it.")
		} else {
			s.skip(s.target(name), "")
		}

		return nil
	}

	if way == syncDownload {
		expected := file
		return &syncTask{kind: taskDownload, key: name, local: filepath.Join(s.root, name), entry: entry, bytes: entry.Size(), expected: &expected}
	}

	if s.tooDeep(name) {
		s.conflict(s.target(name), "The volume cannot hold a path this deep.")
		return nil
	}

	if entry.Version == "" {
		s.conflict(s.target(name), "The remote file has no version to guard its replacement.")
		return nil
	}

	return &syncTask{
		kind: taskUpload, key: name, local: file.full, remote: s.target(name), bytes: file.size, modifiedAt: localAt, replace: true, ifMatch: entry.Version,
	}
}

func (s *syncRun) planLocalOnly(name string, file localEntry) *syncTask {
	if s.mode == syncDownload || !s.accepted(name) {
		return nil
	}

	localAt := math.Floor(file.mtimeMs)

	// Deleted in the volume since the last sync: it stays deleted, unless it changed here since.
	if before, isKnown := s.history[name]; isKnown && !s.options.Force {
		if unchanged(before, float64(file.size), localAt) {
			s.skip(s.target(name), "Deleted in the volume since the last sync.")
		} else {
			s.conflict(s.target(name), "Deleted in the volume and changed here since the last sync; pass --force to send it again.")
		}

		return nil
	}

	if s.tooDeep(name) {
		s.conflict(s.target(name), "The volume cannot hold a path this deep.")
		return nil
	}

	return &syncTask{kind: taskUpload, key: name, local: file.full, remote: s.target(name), bytes: file.size, modifiedAt: localAt}
}

func (s *syncRun) planRemoteOnly(name string, entry files.FileEntry) *syncTask {
	// A staging file — this CLI's, or an upload still in progress — is never a file to keep.
	if remoteStagingPattern.MatchString(entry.Name) || entry.Name == ".featherless-uploads" {
		s.skip(s.target(name), "A staging file of an unfinished transfer is not synchronized.")
		return nil
	}

	if s.mode == syncUpload || !s.accepted(name) {
		return nil
	}

	// Deleted on this computer since the last sync: it stays deleted, unless it changed in the volume since.
	if before, isKnown := s.history[name]; isKnown && !s.options.Force {
		if unchanged(before, entrySize(entry), parsedTime(entry.ModifiedAt)) {
			s.skip(s.target(name), "Deleted on this computer since the last sync.")
		} else {
			s.conflict(s.target(name), "Deleted on this computer and changed in the volume since the last sync; pass --force to fetch it again.")
		}

		return nil
	}

	return &syncTask{kind: taskDownload, key: name, local: filepath.Join(s.root, name), entry: entry, bytes: entry.Size()}
}

// show draws the running total; callers hold mu.
func (s *syncRun) show(isForced bool) {
	if !s.display.isLive || s.total.Files == 0 || !s.display.isDue(isForced) {
		return
	}

	done := count{Files: s.doneFiles}

	for _, bytes := range s.moved {
		done.Bytes += bytes
	}

	s.display.draw(done, s.total)
}

// perform runs one task a worker pulled, keeping the running total.
func (s *syncRun) perform(task *syncTask) {
	if task.isTransfer() {
		s.mu.Lock()
		s.total.Files++
		s.total.Bytes += task.bytes
		s.show(!s.isDeclared)
		s.isDeclared = true
		s.mu.Unlock()
	}

	isDone := s.run(task)
	s.mu.Lock()
	defer s.mu.Unlock()

	if task.isTransfer() {
		s.doneFiles++

		// A finished transfer counts whole; a failed one keeps whatever
		// progress its last report showed.
		if isDone && !s.options.DryRun {
			s.moved[task] = task.bytes
		}
	}

	s.show(false)
}

func (s *syncRun) fail(path, shownPath string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.report.Failed = append(s.report.Failed, syncFailure{Path: path, Error: errs.Describe(err)})
	s.sayErr(shownPath + ": " + errs.Describe(err))
}

// run runs one planned task, recording what it did or why it could not.
func (s *syncRun) run(task *syncTask) bool {
	volumeName := s.data.VolumeName

	switch task.kind {
	case taskMkdirRemote:
		return s.makeFolder(task.remote, shown(volumeName, task.remote), func() error {
			_, err := s.data.Files.Mkdir(s.ctx, task.remote, true)
			return err
		})
	case taskMkdirLocal:
		return s.makeFolder(task.local, task.local, func() error {
			return os.MkdirAll(task.local, 0o777)
		})
	case taskUpload:
		return s.upload(task)
	default:
		return s.download(task)
	}
}

func (s *syncRun) makeFolder(path, shownPath string, create func() error) bool {
	if s.options.DryRun {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.report.Directories++
		s.record(syncAction{Path: path, Action: actionWouldCreateDirectory})
		s.sayOut("would create folder " + shownPath)

		return true
	}

	if err := create(); err != nil {
		s.fail(path, shownPath, err)
		return false
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.report.Directories++
	s.record(syncAction{Path: path, Action: actionCreatedDirectory})

	if s.options.Verbose {
		s.sayOut("created folder " + shownPath)
	}

	return true
}

func (s *syncRun) upload(task *syncTask) bool {
	destination := shown(s.data.VolumeName, task.remote)

	if s.options.DryRun {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.report.Planned++
		s.record(syncAction{Path: task.remote, Action: actionWouldUpload})
		s.sayOut("would upload " + task.local + " -> " + destination)

		return true
	}

	mode := files.ModeCreate

	if task.replace {
		mode = files.ModeOverwrite
	}

	onProgress := func(sent int64) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.moved[task] = sent
	}

	err := s.data.Files.Upload(s.ctx, task.local, task.remote, files.UploadOptions{Mode: mode, Parents: true, IfMatch: task.ifMatch, OnProgress: onProgress})
	var refused *files.FileAPIError

	if errors.As(err, &refused) && refused.Status == 412 {
		reason := "The remote file changed while syncing."

		if refused.Code == "already_exists" {
			reason = "A file appeared while syncing."
		}

		s.conflict(task.remote, reason)

		return false
	}

	if err != nil {
		s.fail(task.remote, destination, err)
		return false
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.synced[task.key] = synced{Size: float64(task.bytes), ModifiedAt: task.modifiedAt}
	s.report.Uploaded++
	s.report.UploadedBytes += task.bytes
	s.record(syncAction{Path: task.remote, Action: actionUploaded})

	if s.options.Verbose {
		s.sayOut(task.local + " -> " + destination)
	}

	return true
}

func (s *syncRun) download(task *syncTask) bool {
	source := shown(s.data.VolumeName, task.entry.Path)

	if s.options.DryRun {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.report.Planned++
		s.record(syncAction{Path: task.entry.Path, Action: actionWouldDownload})
		s.sayOut("would download " + source + " -> " + task.local)

		return true
	}

	// The listing that planned this download read the local side as it was;
	// refuse to replace anything that is there now, whatever the plan said.
	now, err := os.Lstat(task.local)

	if task.expected != nil {
		expected := task.expected
		isSame := err == nil && os.SameFile(now, expected.info) && now.Size() == expected.size && files.Milliseconds(now.ModTime()) == expected.mtimeMs

		if !isSame {
			s.conflict(task.entry.Path, "The local file changed while syncing.")
			return false
		}
	} else if err == nil {
		s.conflict(task.entry.Path, "A local file or folder appeared while syncing.")
		return false
	}

	onProgress := func(received int64) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.moved[task] = received
	}

	if err := s.data.Files.Download(s.ctx, task.entry, task.local, onProgress); err != nil {
		s.fail(task.entry.Path, source, err)
		return false
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.synced[task.key] = synced{Size: float64(task.bytes), ModifiedAt: parsedTime(task.entry.ModifiedAt)}
	s.report.Downloaded++
	s.report.DownloadedBytes += task.bytes
	s.record(syncAction{Path: task.entry.Path, Action: actionDownloaded})

	if s.options.Verbose {
		s.sayOut(source + " -> " + task.local)
	}

	return true
}

func newSyncCommand() *cobra.Command {
	var opts struct {
		mode        string
		dryRun      bool
		mkdirs      bool
		force       bool
		concurrency string
		include     []string
		exclude     []string
		verbose     bool
		progress    bool
		profile     string
	}

	cmd := &cobra.Command{
		Use:   "sync <local-folder> <remote-folder>",
		Short: "Sync a folder with a folder in a volume, sending what changed each way; never deletes",
		Args:  shared.Arguments,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := shared.NewContext(cmd)
			ctx := cmd.Context()
			at, err := volumeAddress(args[1])

			if err != nil {
				return err
			}

			data, err := filesFor(ctx, []string{at.Volume}, opts.profile, acrossOneVolume)

			if err != nil {
				return err
			}

			limit, err := concurrency(opts.concurrency)

			if err != nil {
				return err
			}

			options := syncOptions{
				Mode: syncMode(opts.mode), DryRun: opts.dryRun, Mkdirs: opts.mkdirs, Force: opts.force, Concurrency: limit,
				Include: opts.include, Exclude: opts.exclude, Verbose: opts.verbose, Progress: opts.progress,
			}

			report, err := syncVolumeFolder(ctx, out, data, args[0], at.Path, options)

			if err != nil {
				return err
			}

			emit(out, report, func() []string {
				summary := "Uploaded " + strconv.Itoa(report.Uploaded) + " file(s), " + formatSize(float64(report.UploadedBytes)) +
					"; downloaded " + strconv.Itoa(report.Downloaded) + " file(s), " + formatSize(float64(report.DownloadedBytes)) +
					"; created " + strconv.Itoa(report.Directories) + " folder(s); skipped " + strconv.Itoa(report.Skipped) +
					", conflict(s) " + strconv.Itoa(report.Conflicts) + ", failed " + strconv.Itoa(len(report.Failed)) + "."

				if opts.dryRun {
					summary = "Would upload and download " + strconv.Itoa(report.Planned) + " file(s), create " + strconv.Itoa(report.Directories) +
						" folder(s); skipped " + strconv.Itoa(report.Skipped) + ", conflict(s) " + strconv.Itoa(report.Conflicts) + "."
				}

				lines := []string{summary}

				if report.Truncated {
					lines = append(lines, "… and more actions not listed.")
				}

				return lines
			})

			if len(report.Failed) > 0 || report.Conflicts > 0 {
				return &errs.Exit{Code: 1}
			}

			return nil
		},
	}

	completion.Args(cmd, completion.Arg{Name: "local-folder"}, completion.Arg{Name: "remote-folder", Complete: Paths})
	shared.Option(cmd, &opts.mode, "--mode <mode>", "both", "upload, download or both")
	shared.Switch(cmd, &opts.dryRun, "--dry-run", "show what would be sent, received and created")
	shared.Switch(cmd, &opts.mkdirs, "--mkdirs", "create missing folders on either side")
	shared.Switch(cmd, &opts.force, "--force", "send even when the chosen side is older (needs --mode upload or --mode download)")
	shared.Option(cmd, &opts.concurrency, "--concurrency <n>", "16", "files at a time, 1 to 32")
	shared.Repeatable(cmd, &opts.include, "--include <glob>", "only relative paths matching a glob (*, ** and ?)")
	shared.Repeatable(cmd, &opts.exclude, "--exclude <glob>", "skip relative paths matching a glob (*, ** and ?)")
	shared.Switch(cmd, &opts.verbose, "-v, --verbose", "print each file as it syncs")
	shared.Switch(cmd, &opts.progress, "--progress", "show a running total on stderr")
	shared.Option(cmd, &opts.profile, "--profile <name>", "", "profile to use")
	completion.Flag(cmd, "profile", shared.Profiles)
	shared.HelpAfter(cmd, "\nExamples:\n  featherless-cloud volume sync ./weights vol://datasets/models/\n  featherless-cloud volume sync --mode download --mkdirs . vol://datasets/models\n  featherless-cloud volume sync --dry-run ./ckpt vol://datasets/ckpt")

	return cmd
}
