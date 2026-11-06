package volume

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"featherless.ai/featherless-cloud/internal/api"
	"featherless.ai/featherless-cloud/internal/commands/shared"
	"featherless.ai/featherless-cloud/internal/completion"
	"featherless.ai/featherless-cloud/internal/errs"
	"featherless.ai/featherless-cloud/internal/files"
	"featherless.ai/featherless-cloud/internal/output"
)

// A file server takes 32 transfers per volume at once; every file waits a round trip, so more at
// once is faster up to there.
const (
	maxConcurrency     = 32
	defaultConcurrency = 16
)

const (
	maxVolumes = 3
	// Longest typed prefix that still completes to the scheme, e.g. `vol` → `vol://`.
	schemePrefix    = 3
	addressHint     = "Write volume paths as vol://<volume>/<path>."
	scheme          = completion.Scheme
	acrossOneVolume = "Name paths in one volume at a time."
	sourcesInOne    = "Name sources in one volume at a time."
)

// Seams tests replace.
var (
	// listFolder lists a folder for completion.
	listFolder = func(ctx context.Context, cloud api.Cloud, volumeID, folder string) ([]files.FileEntry, error) {
		client := files.NewClient(cloud, volumeID, files.Options{})
		return client.List(ctx, folder)
	}

	// isTerminal tells whether a stream (stdin, stdout or stderr) is a terminal.
	isTerminal = func(stream any) bool {
		file, ok := stream.(*os.File)
		return ok && term.IsTerminal(int(file.Fd()))
	}
)

var repeatedSlashes = regexp.MustCompile(`/{2,}`)

// remotePath is a volume path as people type it: leading and trailing slashes are dropped.
func remotePath(value string) (string, error) {
	cleaned := repeatedSlashes.ReplaceAllString(strings.Trim(value, "/"), "/")

	for part := range strings.SplitSeq(cleaned, "/") {
		if part == ".." || part == "." {
			return "", errs.New(`"` + value + `" is not a valid volume path.`)
		}
	}

	return cleaned, nil
}

// Paths completes `vol` → `vol://`, then volume names, then folder entries;
// nothing for local paths, so the shell completes files.
func Paths(word string, ctx *completion.Context) ([]string, error) {
	if len(word) >= schemePrefix && len(word) < len(scheme) && strings.HasPrefix(scheme, word) {
		return []string{scheme}, nil
	}

	if !strings.HasPrefix(word, scheme) {
		return []string{}, nil
	}

	cloud, err := ctx.Cloud()

	if err != nil {
		return nil, err
	}

	background := context.Background()
	all, err := cloud.ListVolumes(background)

	if err != nil {
		return nil, err
	}

	rest := word[len(scheme):]
	volumeRef, pathPartial, hasSlash := strings.Cut(rest, "/")

	if !hasSlash {
		needle := strings.ToLower(rest)
		candidates := []string{}

		for _, volume := range all {
			if strings.HasPrefix(strings.ToLower(volume.Name), needle) || strings.HasPrefix(volume.ID, needle) {
				candidates = append(candidates, scheme+volume.Name+"/")
			}
		}

		return candidates, nil
	}

	matched := matchVolumes(all, volumeRef)
	folder := ""
	partial := pathPartial

	if strings.HasSuffix(pathPartial, "/") {
		partial = ""
		folder, err = remotePath(pathPartial)
	} else if cut := strings.LastIndex(pathPartial, "/"); cut != -1 {
		partial = pathPartial[cut+1:]
		folder, err = remotePath(pathPartial[:cut+1])
	}

	if err != nil {
		return nil, err
	}

	candidates := []string{}

	for _, volume := range matched[:min(maxVolumes, len(matched))] {
		entries, err := listFolder(background, cloud, volume.ID, folder)

		if err != nil {
			continue
		}

		for _, entry := range entries {
			if !strings.HasPrefix(entry.Name, partial) {
				continue
			}

			candidate := scheme + volume.Name + "/"

			if folder != "" {
				candidate += folder + "/"
			}

			candidate += entry.Name

			if entry.Type == files.TypeDirectory {
				candidate += "/"
			}

			if !slices.Contains(candidates, candidate) {
				candidates = append(candidates, candidate)
			}
		}
	}

	return candidates, nil
}

// matchVolumes is the volumes a typed volume reference may mean: its exact
// name, else names containing it, and the volume it is the id of.
func matchVolumes(all []api.NetworkVolume, ref string) []api.NetworkVolume {
	var matched []api.NetworkVolume

	for _, volume := range all {
		if volume.Name == ref {
			matched = append(matched, volume)
		}
	}

	if len(matched) == 0 {
		needle := strings.ToLower(ref)

		for _, volume := range all {
			if strings.Contains(strings.ToLower(volume.Name), needle) {
				matched = append(matched, volume)
			}
		}
	}

	if !shared.UUIDPattern.MatchString(ref) {
		return matched
	}

	for _, volume := range all {
		isListed := slices.ContainsFunc(matched, func(item api.NetworkVolume) bool { return item.ID == volume.ID })

		if volume.ID == strings.ToLower(ref) && !isListed {
			return append(matched, volume)
		}
	}

	return matched
}

type address struct {
	Volume string
	Path   string
	// Folder is the volume root, or a path written with a trailing slash.
	Folder bool
}

// parseAddress reads `vol://<volume>/<path>`; nil for a path on this computer.
func parseAddress(value string) (*address, error) {
	if !strings.HasPrefix(value, scheme) {
		return nil, nil
	}

	rest := value[len(scheme):]
	volume, raw, _ := strings.Cut(rest, "/")
	cleaned, err := remotePath(raw)

	if err != nil {
		return nil, err
	}

	return &address{Volume: volume, Path: cleaned, Folder: cleaned == "" || strings.HasSuffix(raw, "/")}, nil
}

func volumeAddress(value string) (address, error) {
	parsed, err := parseAddress(value)

	if err != nil {
		return address{}, err
	}

	if parsed == nil || parsed.Volume == "" {
		return address{}, errs.New(`"` + value + `" is not a volume path. ` + addressHint)
	}

	return *parsed, nil
}

func shown(volumeName, target string) string {
	return scheme + volumeName + "/" + target
}

type planKind string

const (
	planUpload   planKind = "upload"
	planDownload planKind = "download"
	planWithin   planKind = "within"
)

// plan is which way a cp or mv goes.
type plan struct {
	Kind planKind
	// LocalSources are an upload's paths on this computer.
	LocalSources []string
	// VolumeSources are a download's, or a copy within volumes'.
	VolumeSources []address
	// LocalDestination is a download's.
	LocalDestination string
	// VolumeDestination is an upload's, or a copy within volumes'.
	VolumeDestination address
}

// direction reads which way a cp or mv goes; the last path is the destination.
func direction(paths []string) (plan, error) {
	if len(paths) < 2 {
		return plan{}, errs.New("Name a source and a destination.")
	}

	addresses := make([]*address, len(paths))

	for index, value := range paths {
		if !strings.HasPrefix(value, scheme) {
			continue
		}

		parsed, err := volumeAddress(value)

		if err != nil {
			return plan{}, err
		}

		addresses[index] = &parsed
	}

	last := len(paths) - 1
	destination := addresses[last]
	var remote []address

	for _, item := range addresses[:last] {
		if item != nil {
			remote = append(remote, *item)
		}
	}

	if len(remote) > 0 && len(remote) != last {
		return plan{}, errs.New("Sources must be all on this computer or all in a volume.")
	}

	if destination == nil && len(remote) == 0 {
		return plan{}, errs.New("Neither side is a volume. " + addressHint)
	}

	if destination == nil {
		return plan{Kind: planDownload, VolumeSources: remote, LocalDestination: paths[last]}, nil
	}

	if len(remote) > 0 {
		return plan{Kind: planWithin, VolumeSources: remote, VolumeDestination: *destination}, nil
	}

	return plan{Kind: planUpload, LocalSources: paths[:last], VolumeDestination: *destination}, nil
}

// under is a copied file's path below the destination, by cp's rules: into a
// folder each source keeps its own name, otherwise the one source takes the
// destination's. A volume root has no name, so its contents are copied.
func under(intoFolder bool, sourceName, rest string) string {
	name := ""

	if intoFolder {
		name = sourceName
	}

	return joinPath(name, rest)
}

func requireFolder(intoFolder bool, sources int, destination string) error {
	if !intoFolder && sources > 1 {
		return errs.New(destination + " is not a folder. Several sources need a folder; end it with /.")
	}

	return nil
}

// joinPath joins volume path parts, leaving out empty ones.
func joinPath(parts ...string) string {
	var kept []string

	for _, part := range parts {
		if part != "" {
			kept = append(kept, part)
		}
	}

	return strings.Join(kept, "/")
}

func lastPart(path string) string {
	return path[strings.LastIndex(path, "/")+1:]
}

// toFixed1 is JavaScript's toFixed(1): ties round up, not to even.
func toFixed1(value float64) string {
	scaled := new(big.Float).SetPrec(256).SetFloat64(value)
	scaled.Mul(scaled, big.NewFloat(10))
	scaled.Add(scaled, big.NewFloat(0.5))
	tenths, _ := scaled.Int(nil)
	whole, fraction := new(big.Int).QuoRem(tenths, big.NewInt(10), new(big.Int))

	return whole.String() + "." + fraction.String()
}

func formatSize(bytes float64) string {
	units := []string{"B", "KiB", "MiB", "GiB", "TiB"}
	value := bytes
	unit := 0

	for value >= 1024 && unit < len(units)-1 {
		value /= 1024
		unit++
	}

	if unit == 0 {
		return strconv.FormatFloat(math.Floor(value+0.5), 'f', -1, 64) + " " + units[unit]
	}

	return toFixed1(value) + " " + units[unit]
}

// jsNumber reads a value as JavaScript's Number() does; NaN when it is not a number.
func jsNumber(value string) float64 {
	text := strings.TrimSpace(value)

	if text == "" {
		return 0
	}

	bases := map[string]int{"0x": 16, "0X": 16, "0o": 8, "0O": 8, "0b": 2, "0B": 2}

	if base, ok := bases[text[:min(2, len(text))]]; ok {
		number, err := strconv.ParseUint(text[2:], base, 64)

		if err != nil {
			return math.NaN()
		}

		return float64(number)
	}

	switch text {
	case "Infinity", "+Infinity":
		return math.Inf(1)
	case "-Infinity":
		return math.Inf(-1)
	}

	if strings.Trim(text, "0123456789+-.eE") != "" {
		return math.NaN()
	}

	number, err := strconv.ParseFloat(text, 64)

	if err != nil && !errors.Is(err, strconv.ErrRange) {
		return math.NaN()
	}

	return number
}

func isInteger(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value == math.Trunc(value)
}

var sizePattern = regexp.MustCompile(`(?i)^([+-])(\d+(?:\.\d+)?)([KMGT]?)(?:i?B)?$`)

var sizeUnits = map[string]float64{"": 1, "K": 1 << 10, "M": 1 << 20, "G": 1 << 30, "T": 1 << 40}

// parseSizes reads `--size` bounds as find writes them: +N for more than N, -N
// for less, in bytes or K, M, G and T. Only the size fields are set.
func parseSizes(values []string) (files.FindFilters, error) {
	var bounds files.FindFilters

	for _, value := range values {
		match := sizePattern.FindStringSubmatch(value)

		if match == nil {
			return files.FindFilters{}, errs.New(`"` + value + `" is not a size bound. Write +N for more than N or -N for less, like +10G or -512K.`)
		}

		number, _ := strconv.ParseFloat(match[2], 64)
		bytes := int64(math.Floor(number*sizeUnits[strings.ToUpper(match[3])] + 0.5))

		if match[1] == "+" {
			minSize := bytes

			if bounds.MinSize != nil {
				minSize = max(*bounds.MinSize, bytes)
			}

			bounds.MinSize = &minSize
		} else {
			maxSize := bytes

			if bounds.MaxSize != nil {
				maxSize = min(*bounds.MaxSize, bytes)
			}

			bounds.MaxSize = &maxSize
		}
	}

	return bounds, nil
}

var agePattern = regexp.MustCompile(`^(\d+(?:\.\d+)?)([smhdw])$`)

var ageUnits = map[string]time.Duration{"s": time.Second, "m": time.Minute, "h": time.Hour, "d": 24 * time.Hour, "w": 7 * 24 * time.Hour}

// parseAge reads an age like 30m, 12h or 7d.
func parseAge(value string) (time.Duration, error) {
	match := agePattern.FindStringSubmatch(value)

	if match == nil {
		return 0, errs.New(`"` + value + `" is not an age. Write a number and a unit, like 30m, 12h or 7d.`)
	}

	number, _ := strconv.ParseFloat(match[1], 64)

	return time.Duration(number * float64(ageUnits[match[2]])), nil
}

type listingLine struct {
	At string
	// Bytes is nil for folders.
	Bytes *int64
	Name  string
}

func localStamp(at string) string {
	milliseconds, ok := files.ParseTime(at)

	if !ok {
		return "NaN-NaN-NaN NaN:NaN:NaN"
	}

	return time.UnixMilli(milliseconds).Local().Format("2006-01-02 15:04:05")
}

// listing renders lines like `aws s3 ls`: local time, size (blank for folders), name.
func listing(lines []listingLine, human bool) []string {
	sizes := make([]string, len(lines))
	width := 0

	for index, line := range lines {
		if line.Bytes == nil {
			continue
		}

		if human {
			sizes[index] = formatSize(float64(*line.Bytes))
		} else {
			sizes[index] = strconv.FormatInt(*line.Bytes, 10)
		}

		width = max(width, len(sizes[index]))
	}

	rendered := make([]string, len(lines))

	for index, line := range lines {
		padding := strings.Repeat(" ", width-len(sizes[index]))
		rendered[index] = localStamp(line.At) + "  " + padding + sizes[index] + "  " + line.Name
	}

	return rendered
}

type count struct {
	Files int
	Bytes int64
}

func progressLine(verb string, done, total count, elapsed time.Duration) string {
	rate := ""

	if elapsed >= time.Second {
		seconds := float64(elapsed.Milliseconds()) / 1000
		rate = ", " + formatSize(float64(done.Bytes)/seconds) + "/s"
	}

	doneSize := formatSize(float64(done.Bytes))
	totalSize := formatSize(float64(total.Bytes))

	return fmt.Sprintf("%s %d/%d files, %s of %s%s", verb, done.Files, total.Files, doneSize, totalSize, rate)
}

func printLine(stream io.Writer, line string) error {
	_, err := io.WriteString(stream, line+"\n")
	return err
}

// emit prints JSON with --json; otherwise plain lines, as filesystem commands print them.
func emit(out *output.Context, data any, lines func() []string) {
	if out.Mode == output.JSON {
		printLine(out.Stdout, output.EncodeJSON(data, true))
		return
	}

	for _, line := range lines() {
		if err := printLine(out.Stdout, line); err != nil {
			return
		}
	}
}

type volumeFiles struct {
	Files      *files.Client
	VolumeName string
}

// filesFor is one file client for the volume every reference names.
func filesFor(ctx context.Context, refs []string, profile, acrossVolumes string) (volumeFiles, error) {
	cloud, err := shared.Client(profile)

	if err != nil {
		return volumeFiles{}, err
	}

	volumes, err := cloud.ListVolumes(ctx)

	if err != nil {
		return volumeFiles{}, err
	}

	var found []api.NetworkVolume
	var seen []string

	for _, ref := range refs {
		if slices.Contains(seen, ref) {
			continue
		}

		seen = append(seen, ref)
		volume, err := shared.FindVolume(volumes, ref)

		if err != nil {
			return volumeFiles{}, err
		}

		found = append(found, volume)
	}

	for _, volume := range found {
		if volume.ID != found[0].ID {
			return volumeFiles{}, errs.New(acrossVolumes)
		}
	}

	return volumeFiles{Files: files.NewClient(cloud, found[0].ID, files.Options{}), VolumeName: found[0].Name}, nil
}

func concurrency(value string) (int, error) {
	parsed := jsNumber(value)

	if !isInteger(parsed) || parsed < 1 || parsed > maxConcurrency {
		return 0, errs.Newf("--concurrency must be 1 to %d.", maxConcurrency)
	}

	return int(parsed), nil
}

// eachConcurrently runs work on every index, limit at a time, stopping at the first failure.
func eachConcurrently(items int, limit int, work func(index int) error) error {
	var mu sync.Mutex
	next := 0
	var failure error
	var group sync.WaitGroup

	for range min(limit, items) {
		group.Go(func() {
			for {
				mu.Lock()

				if failure != nil || next >= items {
					mu.Unlock()
					return
				}

				index := next
				next++
				mu.Unlock()

				if err := work(index); err != nil {
					mu.Lock()

					if failure == nil {
						failure = err
					}

					mu.Unlock()
				}
			}
		})
	}

	group.Wait()

	return failure
}

// transfer is one planned file; each direction fills the fields it plans
// with, in the order JSON output shows them.
type transfer struct {
	Source  string           `json:"source"`
	Target  string           `json:"target"`
	Remote  string           `json:"remote,omitzero"`
	Bytes   int64            `json:"bytes"`
	MtimeMs float64          `json:"mtimeMs,omitzero"`
	Entry   *files.FileEntry `json:"entry,omitzero"`
	Error   string           `json:"error,omitzero"`
}

type transferReport struct {
	Done    []transfer `json:"done"`
	Skipped []transfer `json:"skipped"`
	Failed  []transfer `json:"failed"`
}

type copyOptions struct {
	Limit     int
	Recursive bool
	Overwrite bool
	Update    bool
	DryRun    bool
	// Move deletes each source once its copy finished.
	Move bool
	// Verbose prints each file as it finishes.
	Verbose bool
	// Progress shows a running total on stderr.
	Progress bool
}

// progressDisplay is the running total on stderr: redrawn in place on a
// terminal, a line every few seconds otherwise. Callers serialize its use.
type progressDisplay struct {
	out        *output.Context
	verb       string
	isLive     bool
	isTerminal bool
	started    time.Time
	shownAt    time.Time
	isDrawn    bool
}

func newProgressDisplay(out *output.Context, verb string, isLive bool) *progressDisplay {
	return &progressDisplay{out: out, verb: verb, isLive: isLive, isTerminal: isTerminal(out.Stderr), started: out.Now()}
}

func (p *progressDisplay) isDue(isForced bool) bool {
	if isForced {
		return true
	}

	interval := 5 * time.Second

	if p.isTerminal {
		interval = 250 * time.Millisecond
	}

	return p.out.Now().Sub(p.shownAt) >= interval
}

func (p *progressDisplay) draw(done, total count) {
	p.shownAt = p.out.Now()
	line := progressLine(p.verb, done, total, p.out.Now().Sub(p.started))

	if p.isTerminal {
		io.WriteString(p.out.Stderr, "\r\x1b[K"+line)
		p.isDrawn = true
		return
	}

	printLine(p.out.Stderr, line)
}

// say prints a line, first clearing a drawn total.
func (p *progressDisplay) say(stream io.Writer, line string) {
	if p.isDrawn {
		io.WriteString(p.out.Stderr, "\r\x1b[K")
		p.isDrawn = false
	}

	printLine(stream, line)
}

func (p *progressDisplay) finish() {
	if p.isDrawn {
		io.WriteString(p.out.Stderr, "\n")
	}
}

// runTransfers runs transfers like cp: silent on success, each failure on
// stderr, each file with verbose, and a running total on stderr with progress.
func runTransfers(out *output.Context, verb string, transfers []transfer, options copyOptions, move func(item transfer, progress func(int64)) error) transferReport {
	report := transferReport{Done: []transfer{}, Skipped: []transfer{}, Failed: []transfer{}}
	isJSON := out.Mode == output.JSON
	total := count{Files: len(transfers)}

	for _, item := range transfers {
		total.Bytes += item.Bytes
	}

	var mu sync.Mutex
	moved := make([]int64, len(transfers))
	display := newProgressDisplay(out, verb, options.Progress && !isJSON && len(transfers) > 0)
	isShownLast := false

	// show draws the total; callers hold mu.
	show := func(isForced bool) {
		if !display.isLive || isShownLast || !display.isDue(isForced) {
			return
		}

		isShownLast = len(report.Done)+len(report.Failed) == len(transfers)
		done := count{Files: len(report.Done)}

		for _, bytes := range moved {
			done.Bytes += bytes
		}

		display.draw(done, total)
	}

	mu.Lock()
	show(true)
	mu.Unlock()

	eachConcurrently(len(transfers), options.Limit, func(index int) error {
		item := transfers[index]

		err := move(item, func(bytes int64) {
			mu.Lock()
			defer mu.Unlock()
			moved[index] = bytes
			show(false)
		})

		mu.Lock()
		defer mu.Unlock()

		if err == nil {
			moved[index] = item.Bytes
			report.Done = append(report.Done, item)

			if options.Verbose && !isJSON {
				display.say(out.Stdout, item.Source+" -> "+item.Target)
			}
		} else {
			failed := item
			failed.Error = errs.Describe(err)
			report.Failed = append(report.Failed, failed)

			if !isJSON {
				display.say(out.Stderr, item.Source+": "+failed.Error)
			}
		}

		show(false)

		return nil
	})

	mu.Lock()
	show(true)
	display.finish()
	mu.Unlock()

	return report
}

// finish prints a cp or mv report; failures end the command with exit code 1.
func finish(out *output.Context, report transferReport, dryRun bool) error {
	emit(out, report, func() []string {
		if !dryRun {
			return nil
		}

		var lines []string

		for _, item := range report.Done {
			lines = append(lines, "would copy "+item.Source+" -> "+item.Target)
		}

		for _, item := range report.Skipped {
			lines = append(lines, "would skip "+item.Source+" (unchanged)")
		}

		return lines
	})

	if len(report.Failed) > 0 {
		return &errs.Exit{Code: 1}
	}

	return nil
}

type localFile struct {
	local   string
	rest    string
	size    int64
	mtimeMs float64
}

// localFiles is every file under a local path, with its path below that path ("" for a file).
func localFiles(source string, recursive bool) (isFolder bool, found []localFile, err error) {
	info, statErr := os.Stat(source)

	if statErr != nil {
		return false, nil, errs.New(source + " does not exist.")
	}

	if info.Mode().IsRegular() {
		return false, []localFile{{local: source, size: info.Size(), mtimeMs: files.Milliseconds(info.ModTime())}}, nil
	}

	if !info.IsDir() {
		return false, nil, errs.New(source + " is not a file or folder.")
	}

	if !recursive {
		return false, nil, errs.New(source + " is a folder. Pass -r to copy folders.")
	}

	root, err := filepath.Abs(source)

	if err != nil {
		return false, nil, err
	}

	var walk func(folder string) error

	walk = func(folder string) error {
		children, err := os.ReadDir(folder)

		if err != nil {
			return err
		}

		for _, child := range children {
			full := filepath.Join(folder, child.Name())

			if child.IsDir() {
				if err := walk(full); err != nil {
					return err
				}

				continue
			}

			if !child.Type().IsRegular() {
				continue
			}

			info, err := os.Stat(full)

			if err != nil {
				return err
			}

			rest, _ := filepath.Rel(root, full)
			file := localFile{local: full, rest: filepath.ToSlash(rest), size: info.Size(), mtimeMs: files.Milliseconds(info.ModTime())}
			found = append(found, file)
		}

		return nil
	}

	if err := walk(root); err != nil {
		return false, nil, err
	}

	return true, found, nil
}

type remoteFile struct {
	entry files.FileEntry
	rest  string
}

// remoteFiles is every file under a volume path, with its path below that path ("" for a file).
func remoteFiles(ctx context.Context, client *files.Client, source string, recursive bool) (isFolder bool, found []remoteFile, err error) {
	var entry *files.FileEntry

	if source != "" {
		entry, err = client.Entry(ctx, source)

		if err != nil {
			return false, nil, err
		}

		if entry == nil {
			return false, nil, errs.New(source + " does not exist in the volume.")
		}
	}

	if entry != nil && entry.Type == files.TypeFile {
		return false, []remoteFile{{entry: *entry}}, nil
	}

	if !recursive {
		name := source

		if name == "" {
			name = "The volume root"
		}

		return false, nil, errs.New(name + " is a folder. Pass -r to copy folders.")
	}

	prefix := ""

	if source != "" {
		prefix = source + "/"
	}

	var walk func(folder string) error

	walk = func(folder string) error {
		children, err := client.List(ctx, folder)

		if err != nil {
			return err
		}

		for _, child := range children {
			if child.Type == files.TypeDirectory {
				if err := walk(child.Path); err != nil {
					return err
				}

				continue
			}

			found = append(found, remoteFile{entry: child, rest: strings.TrimPrefix(child.Path, prefix)})
		}

		return nil
	}

	if err := walk(source); err != nil {
		return false, nil, err
	}

	return true, found, nil
}

// removeEmptyLocal removes the folders a move emptied, deepest first; folders still holding anything stay.
func removeEmptyLocal(folder string) error {
	children, err := os.ReadDir(folder)

	if err != nil {
		return err
	}

	for _, child := range children {
		if child.IsDir() {
			if err := removeEmptyLocal(filepath.Join(folder, child.Name())); err != nil {
				return err
			}
		}
	}

	os.Remove(folder)

	return nil
}

func removeEmptyRemote(ctx context.Context, client *files.Client, folder string) error {
	children, err := client.List(ctx, folder)

	if err != nil {
		return err
	}

	for _, child := range children {
		if child.Type == files.TypeDirectory {
			if err := removeEmptyRemote(ctx, client, child.Path); err != nil {
				return err
			}
		}
	}

	err = client.Remove(ctx, folder, false)
	var refused *files.FileAPIError
	isNotEmpty := errors.As(err, &refused) && refused.Code == "directory_not_empty"

	if err != nil && !isNotEmpty {
		return err
	}

	return nil
}

// sameModifiedTime tells whether a local file and a volume file carry the same
// modification time. The volume keeps whole milliseconds, and copies keep the
// local time cut to the millisecond, so a copy always matches.
func sameModifiedTime(localMs float64, modifiedAt string) bool {
	remote, ok := files.ParseTime(modifiedAt)
	return ok && math.Floor(localMs) == float64(remote)
}

func refusalCode(err error) string {
	var refused *files.FileAPIError

	if errors.As(err, &refused) {
		return refused.Code
	}

	return ""
}

// splitUnchanged sets aside, with --update, the files isSame finds already in
// place, keeping the planned order.
func splitUnchanged(planned []transfer, options copyOptions, limit int, isSame func(item transfer) (bool, error)) (pending, skipped []transfer, err error) {
	if !options.Update {
		return planned, []transfer{}, nil
	}

	same := make([]bool, len(planned))

	err = eachConcurrently(len(planned), limit, func(index int) error {
		isUnchanged, err := isSame(planned[index])
		same[index] = isUnchanged

		return err
	})

	if err != nil {
		return nil, nil, err
	}

	pending = []transfer{}
	skipped = []transfer{}

	for index, item := range planned {
		if same[index] {
			skipped = append(skipped, item)
		} else {
			pending = append(pending, item)
		}
	}

	return pending, skipped, nil
}

// intoFolderAt tells whether a volume destination is a folder to copy into.
func intoFolderAt(ctx context.Context, client *files.Client, destination address) (bool, error) {
	if destination.Folder {
		return true, nil
	}

	existing, err := client.Entry(ctx, destination.Path)

	if err != nil {
		return false, err
	}

	return existing != nil && existing.Type == files.TypeDirectory, nil
}

func existsHint(isMove bool) string {
	if isMove {
		return ""
	}

	return " or --update"
}

// upload copies files from this computer into a volume.
func upload(ctx context.Context, out *output.Context, data volumeFiles, sources []string, destination address, options copyOptions) (transferReport, error) {
	intoFolder, err := intoFolderAt(ctx, data.Files, destination)

	if err != nil {
		return transferReport{}, err
	}

	if err := requireFolder(intoFolder, len(sources), shown(data.VolumeName, destination.Path)); err != nil {
		return transferReport{}, err
	}

	planned := []transfer{}
	var folders []string

	for _, source := range sources {
		isFolder, found, err := localFiles(source, options.Recursive || options.Move)

		if err != nil {
			return transferReport{}, err
		}

		if isFolder {
			folders = append(folders, source)
		}

		absolute, _ := filepath.Abs(source)

		for _, file := range found {
			remote := joinPath(destination.Path, under(intoFolder, filepath.Base(absolute), file.rest))
			item := transfer{Source: file.local, Target: shown(data.VolumeName, remote), Remote: remote, Bytes: file.size, MtimeMs: file.mtimeMs}
			planned = append(planned, item)
		}
	}

	pending, skipped, err := splitUnchanged(planned, options, options.Limit, func(item transfer) (bool, error) {
		existing, err := data.Files.Entry(ctx, item.Remote)

		if err != nil {
			return false, err
		}

		isFile := existing != nil && existing.Type == files.TypeFile && existing.SizeBytes != nil

		return isFile && *existing.SizeBytes == item.Bytes && sameModifiedTime(item.MtimeMs, existing.ModifiedAt), nil
	})

	if err != nil {
		return transferReport{}, err
	}

	if options.DryRun {
		return transferReport{Done: pending, Skipped: skipped, Failed: []transfer{}}, nil
	}

	mode := files.ModeCreate

	if options.Overwrite || options.Update {
		mode = files.ModeOverwrite
	}

	report := runTransfers(out, "Uploading", pending, options, func(item transfer, progress func(int64)) error {
		err := data.Files.Upload(ctx, item.Source, item.Remote, files.UploadOptions{Mode: mode, Parents: true, OnProgress: progress})

		if refusalCode(err) == "already_exists" {
			return errs.New("already exists in the volume (pass --overwrite" + existsHint(options.Move) + ")")
		}

		if err != nil {
			return err
		}

		if options.Move {
			return os.Remove(item.Source)
		}

		return nil
	})

	if options.Move {
		for _, folder := range folders {
			if err := removeEmptyLocal(folder); err != nil {
				return transferReport{}, err
			}
		}
	}

	report.Skipped = skipped

	return report, nil
}

// download copies files from a volume to this computer.
func download(ctx context.Context, out *output.Context, data volumeFiles, sources []address, destination string, options copyOptions) (transferReport, error) {
	existing, statErr := os.Stat(destination)
	hasSeparator := strings.HasSuffix(destination, "/") || strings.HasSuffix(destination, `\`)
	intoFolder := hasSeparator || (statErr == nil && existing.IsDir())

	if err := requireFolder(intoFolder, len(sources), destination); err != nil {
		return transferReport{}, err
	}

	planned := []transfer{}
	var folders []string

	for _, source := range sources {
		isFolder, found, err := remoteFiles(ctx, data.Files, source.Path, options.Recursive || options.Move)

		if err != nil {
			return transferReport{}, err
		}

		if isFolder {
			folders = append(folders, source.Path)
		}

		for _, file := range found {
			entry := file.entry
			below := under(intoFolder, lastPart(source.Path), file.rest)
			target := filepath.Join(destination, filepath.FromSlash(below))
			planned = append(planned, transfer{Source: shown(data.VolumeName, entry.Path), Target: target, Bytes: entry.Size(), Entry: &entry})
		}
	}

	pending, skipped, err := splitUnchanged(planned, options, 1, func(item transfer) (bool, error) {
		local, err := os.Stat(item.Target)

		if err != nil {
			return false, nil
		}

		isFile := local.Mode().IsRegular() && local.Size() == item.Bytes

		return isFile && sameModifiedTime(files.Milliseconds(local.ModTime()), item.Entry.ModifiedAt), nil
	})

	if err != nil {
		return transferReport{}, err
	}

	if options.DryRun {
		return transferReport{Done: pending, Skipped: skipped, Failed: []transfer{}}, nil
	}

	report := runTransfers(out, "Downloading", pending, options, func(item transfer, progress func(int64)) error {
		if err := data.Files.Download(ctx, *item.Entry, item.Target, progress); err != nil {
			return err
		}

		if options.Move {
			return data.Files.Remove(ctx, item.Entry.Path, false)
		}

		return nil
	})

	if options.Move {
		for _, folder := range folders {
			if err := removeEmptyRemote(ctx, data.Files, folder); err != nil {
				return transferReport{}, err
			}
		}
	}

	report.Skipped = skipped

	return report, nil
}

// between copies or moves files from one volume into another on the file
// server; nothing passes through this computer.
func between(ctx context.Context, out *output.Context, source volumeFiles, sourcePaths []address, target volumeFiles, destination address, options copyOptions) (transferReport, error) {
	intoFolder, err := intoFolderAt(ctx, target.Files, destination)

	if err != nil {
		return transferReport{}, err
	}

	if err := requireFolder(intoFolder, len(sourcePaths), shown(target.VolumeName, destination.Path)); err != nil {
		return transferReport{}, err
	}

	planned := []transfer{}
	var folders []string

	for _, from := range sourcePaths {
		isFolder, found, err := remoteFiles(ctx, source.Files, from.Path, options.Recursive || options.Move)

		if err != nil {
			return transferReport{}, err
		}

		if isFolder {
			folders = append(folders, from.Path)
		}

		for _, file := range found {
			entry := file.entry
			remote := joinPath(destination.Path, under(intoFolder, lastPart(from.Path), file.rest))

			planned = append(planned, transfer{
				Source: shown(source.VolumeName, entry.Path), Target: shown(target.VolumeName, remote), Remote: remote, Bytes: entry.Size(), Entry: &entry,
			})
		}
	}

	pending, skipped, err := splitUnchanged(planned, options, options.Limit, func(item transfer) (bool, error) {
		existing, err := target.Files.Entry(ctx, item.Remote)

		if err != nil {
			return false, err
		}

		isFile := existing != nil && existing.Type == files.TypeFile && existing.SizeBytes != nil

		return isFile && *existing.SizeBytes == item.Bytes && existing.ModifiedAt == item.Entry.ModifiedAt, nil
	})

	if err != nil {
		return transferReport{}, err
	}

	if options.DryRun {
		return transferReport{Done: pending, Skipped: skipped, Failed: []transfer{}}, nil
	}

	verb := "Copying"

	if options.Move {
		verb = "Moving"
	}

	report := runTransfers(out, verb, pending, options, func(item transfer, _ func(int64)) error {
		copied := files.CopyOptions{Overwrite: options.Overwrite || options.Update, Move: options.Move}
		_, err := target.Files.CopyFrom(ctx, source.Files.VolumeID(), item.Entry.Path, item.Remote, copied)

		switch refusalCode(err) {
		case "already_exists":
			return errs.New("already exists in the destination (pass --overwrite" + existsHint(options.Move) + ")")
		// The file service only copies between volumes it can reach together.
		case "volume_not_found":
			return errs.New(source.VolumeName + " and " + target.VolumeName + " can't be copied between directly. Copy the files to this computer, then into the other volume.")
		}

		return err
	})

	if options.Move {
		for _, folder := range folders {
			if err := removeEmptyRemote(ctx, source.Files, folder); err != nil {
				return transferReport{}, err
			}
		}
	}

	report.Skipped = skipped

	return report, nil
}

type findOptions struct {
	Long   bool
	Human  bool
	Print0 bool
}

// find prints each match as a vol:// address as the file server finds it, or,
// with Long or --json, every match once the search finished.
func find(ctx context.Context, out *output.Context, data volumeFiles, at address, filters files.FindFilters, options findOptions) error {
	name := func(entry files.FileEntry) string {
		if entry.Type == files.TypeDirectory {
			return shown(data.VolumeName, entry.Path) + "/"
		}

		return shown(data.VolumeName, entry.Path)
	}

	if out.Mode != output.JSON && !options.Long {
		end := "\n"

		if options.Print0 {
			end = "\x00"
		}

		for entry, err := range data.Files.Find(ctx, at.Path, filters) {
			if err != nil {
				return err
			}

			// A reader like head may close the pipe early; stop searching then rather than fail.
			if _, err := io.WriteString(out.Stdout, name(entry)+end); err != nil {
				if errors.Is(err, syscall.EPIPE) {
					return nil
				}

				return err
			}
		}

		return nil
	}

	entries := []files.FileEntry{}

	for entry, err := range data.Files.Find(ctx, at.Path, filters) {
		if err != nil {
			return err
		}

		entries = append(entries, entry)
	}

	emit(out, entries, func() []string {
		lines := make([]listingLine, len(entries))

		for index, entry := range entries {
			lines[index] = entryLine(entry, name(entry))
		}

		return listing(lines, options.Human)
	})

	return nil
}

// entryLine lists an entry under a name; folders have no size.
func entryLine(entry files.FileEntry, name string) listingLine {
	if entry.Type == files.TypeDirectory {
		return listingLine{At: entry.ModifiedAt, Name: name}
	}

	size := entry.Size()

	return listingLine{At: entry.ModifiedAt, Bytes: &size, Name: name}
}

func newLsCommand() *cobra.Command {
	var opts struct {
		humanReadable bool
		recursive     bool
		summarize     bool
		all           bool
		long          bool
		profile       string
	}

	cmd := &cobra.Command{
		Use:   "ls [path]",
		Short: "List your volumes, or a folder in one (vol://<volume>/<path>)",
		Args:  shared.Arguments,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := shared.NewContext(cmd)
			ctx := cmd.Context()
			target := ""

			if len(args) > 0 {
				target = args[0]
			}

			if target == "" || target == scheme {
				return listVolumes(ctx, out, opts.profile, opts.humanReadable)
			}

			at, err := volumeAddress(target)

			if err != nil {
				return err
			}

			data, err := filesFor(ctx, []string{at.Volume}, opts.profile, acrossOneVolume)

			if err != nil {
				return err
			}

			return listFolderEntries(ctx, out, data.Files, at, opts.recursive, opts.summarize, opts.humanReadable)
		},
	}

	completion.Args(cmd, completion.Arg{Name: "path", Complete: Paths})
	shared.Switch(cmd, &opts.humanReadable, "-h, --human-readable", "sizes like 1.5 KiB and 100.0 MiB instead of bytes")
	shared.Switch(cmd, &opts.recursive, "-r, --recursive", "list everything below the folder, by path from the volume root")
	shared.Switch(cmd, &opts.summarize, "--summarize", "end with the number of files and their total size")
	// Listings always show details and hidden files; these keep `ls -al` habits working.
	shared.Switch(cmd, &opts.all, "-a, --all", "")
	shared.Switch(cmd, &opts.long, "-l, --long", "")
	cmd.Flags().MarkHidden("all")
	cmd.Flags().MarkHidden("long")
	shared.Option(cmd, &opts.profile, "--profile <name>", "", "profile to use")
	completion.Flag(cmd, "profile", shared.Profiles)
	helpWithoutShorthand(cmd)

	return cmd
}

// helpWithoutShorthand gives --help no -h, which -h, --human-readable takes, as commander did.
func helpWithoutShorthand(cmd *cobra.Command) {
	cmd.Flags().Bool("help", false, "display help for command")
}

// folderLine lists an entry of a folder listing, folders marked with a slash.
func folderLine(entry files.FileEntry, name string) listingLine {
	if entry.Type == files.TypeDirectory {
		return entryLine(entry, name+"/")
	}

	return entryLine(entry, name)
}

func listVolumes(ctx context.Context, out *output.Context, profile string, human bool) error {
	cloud, err := shared.Client(profile)

	if err != nil {
		return err
	}

	volumes, err := cloud.ListVolumes(ctx)

	if err != nil {
		return err
	}

	if volumes == nil {
		volumes = []api.NetworkVolume{}
	}

	emit(out, volumes, func() []string {
		lines := make([]listingLine, len(volumes))

		for index, volume := range volumes {
			capacity := int64(volume.CapacityGiB) << 30
			lines[index] = listingLine{At: volume.CreatedAt, Bytes: &capacity, Name: scheme + volume.Name}
		}

		return listing(lines, human)
	})

	return nil
}

func listFolderEntries(ctx context.Context, out *output.Context, client *files.Client, at address, recursive, summarize, human bool) error {
	entries := []files.FileEntry{}
	var lines []listingLine

	if recursive {
		var walk func(folder string) error

		walk = func(folder string) error {
			children, err := client.List(ctx, folder)

			if err != nil {
				return err
			}

			for _, child := range children {
				entries = append(entries, child)

				if child.Type == files.TypeDirectory {
					if err := walk(child.Path); err != nil {
						return err
					}
				}
			}

			return nil
		}

		if err := walk(at.Path); err != nil {
			return err
		}

		for _, entry := range entries {
			lines = append(lines, folderLine(entry, entry.Path))
		}
	} else {
		listed, self, above, err := folderAndItsParent(ctx, client, at.Path)

		if err != nil {
			return err
		}

		entries = listed

		if self != nil {
			lines = append(lines, folderLine(*self, "."))
		}

		if above != nil {
			lines = append(lines, folderLine(*above, ".."))
		}

		for _, entry := range entries {
			lines = append(lines, folderLine(entry, entry.Name))
		}
	}

	emit(out, entries, func() []string {
		rendered := listing(lines, human)

		if !summarize {
			return rendered
		}

		fileCount := 0
		total := int64(0)

		for _, entry := range entries {
			if entry.Type == files.TypeFile {
				fileCount++
				total += entry.Size()
			}
		}

		size := strconv.FormatInt(total, 10)

		if human {
			size = formatSize(float64(total))
		}

		return append(rendered, "", "Total Files: "+strconv.Itoa(fileCount), " Total Size: "+size)
	})

	return nil
}

// folderAndItsParent lists a folder and reads its own entry and its parent's, at once.
func folderAndItsParent(ctx context.Context, client *files.Client, folder string) (listed []files.FileEntry, self, above *files.FileEntry, err error) {
	parent := ""

	if cut := strings.LastIndex(folder, "/"); cut != -1 {
		parent = folder[:cut]
	}

	var group sync.WaitGroup
	var listErr, selfErr, aboveErr error

	group.Go(func() { listed, listErr = client.List(ctx, folder) })
	group.Go(func() { self, selfErr = client.Entry(ctx, folder) })

	if folder != "" {
		group.Go(func() { above, aboveErr = client.Entry(ctx, parent) })
	}

	group.Wait()

	for _, failure := range []error{listErr, selfErr, aboveErr} {
		if failure != nil {
			return nil, nil, nil, failure
		}
	}

	return listed, self, above, nil
}

func newFindCommand() *cobra.Command {
	var opts struct {
		name          string
		iname         string
		kind          string
		maxdepth      string
		size          []string
		newerThan     string
		olderThan     string
		long          bool
		humanReadable bool
		print0        bool
		profile       string
	}

	cmd := &cobra.Command{
		Use:   "find <path>",
		Short: "Find files and folders below a folder in a volume (vol://<volume>/<path>), printing each as it is found",
		Args:  shared.Arguments,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := shared.NewContext(cmd)
			flags := cmd.Flags()
			isNamed := flags.Changed("name")
			isIgnoringCase := flags.Changed("iname")

			if isNamed && isIgnoringCase {
				return errs.New("Pass --name or --iname, not both.")
			}

			filters := files.FindFilters{Name: opts.name, IgnoreCase: isIgnoringCase}

			if isIgnoringCase {
				filters.Name = opts.iname
			}

			if flags.Changed("maxdepth") {
				depth := jsNumber(opts.maxdepth)

				if !isInteger(depth) || depth < 1 {
					return errs.New("--maxdepth takes a whole number of levels, 1 or more.")
				}

				levels := int(depth)
				filters.MaxDepth = &levels
			}

			at, err := volumeAddress(args[0])

			if err != nil {
				return err
			}

			now := time.UnixMilli(out.Now().UnixMilli())
			bounds, err := parseSizes(opts.size)

			if err != nil {
				return err
			}

			filters.MinSize = bounds.MinSize
			filters.MaxSize = bounds.MaxSize
			kinds := map[string]files.EntryType{"f": files.TypeFile, "d": files.TypeDirectory}
			filters.Type = kinds[opts.kind]

			if flags.Changed("newer-than") {
				age, err := parseAge(opts.newerThan)

				if err != nil {
					return err
				}

				after := now.Add(-age)
				filters.ModifiedAfter = &after
			}

			if flags.Changed("older-than") {
				age, err := parseAge(opts.olderThan)

				if err != nil {
					return err
				}

				before := now.Add(-age)
				filters.ModifiedBefore = &before
			}

			ctx := cmd.Context()
			data, err := filesFor(ctx, []string{at.Volume}, opts.profile, acrossOneVolume)

			if err != nil {
				return err
			}

			return find(ctx, out, data, at, filters, findOptions{Long: opts.long, Human: opts.humanReadable, Print0: opts.print0})
		},
	}

	completion.Args(cmd, completion.Arg{Name: "path", Complete: Paths})
	shared.Option(cmd, &opts.name, "--name <pattern>", "", `names matching a pattern like "*.safetensors": * matches any characters, ? one, [abc] one of those`)
	shared.Option(cmd, &opts.iname, "--iname <pattern>", "", "like --name, ignoring case")
	shared.Option(cmd, &opts.kind, "--type <f|d>", "", "only files (f) or only folders (d)")
	completion.Choices(cmd, "type", "f", "d")
	shared.Option(cmd, &opts.maxdepth, "--maxdepth <n>", "", "how many levels below the folder to look")
	shared.Repeatable(cmd, &opts.size, "--size <+n|-n>", "more (+) or less (-) than a size like 10G or 512K; repeat for a range")
	shared.Option(cmd, &opts.newerThan, "--newer-than <age>", "", "modified within an age like 30m, 12h or 7d")
	shared.Option(cmd, &opts.olderThan, "--older-than <age>", "", "modified longer ago than an age like 30m, 12h or 7d")
	shared.Switch(cmd, &opts.long, "-l, --long", "print each with its modification time and size, like ls")
	shared.Switch(cmd, &opts.humanReadable, "-h, --human-readable", "with -l, sizes like 1.5 KiB and 100.0 MiB instead of bytes")
	shared.Switch(cmd, &opts.print0, "-0, --print0", "end each with a NUL instead of a newline, for xargs -0")
	shared.Option(cmd, &opts.profile, "--profile <name>", "", "profile to use")
	completion.Flag(cmd, "profile", shared.Profiles)
	helpWithoutShorthand(cmd)

	return cmd
}

// volumeAddresses reads every argument as a volume path.
func volumeAddresses(values []string) ([]address, error) {
	addresses := make([]address, len(values))

	for index, value := range values {
		parsed, err := volumeAddress(value)

		if err != nil {
			return nil, err
		}

		addresses[index] = parsed
	}

	return addresses, nil
}

func volumesOf(addresses []address) []string {
	volumes := make([]string, len(addresses))

	for index, at := range addresses {
		volumes[index] = at.Volume
	}

	return volumes
}

func newMkdirCommand() *cobra.Command {
	var opts struct {
		parents bool
		profile string
	}

	cmd := &cobra.Command{
		Use:   "mkdir <paths...>",
		Short: "Create folders in a volume",
		Args:  shared.Arguments,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := shared.NewContext(cmd)
			ctx := cmd.Context()
			addresses, err := volumeAddresses(args)

			if err != nil {
				return err
			}

			data, err := filesFor(ctx, volumesOf(addresses), opts.profile, acrossOneVolume)

			if err != nil {
				return err
			}

			created := []files.FileEntry{}

			for _, at := range addresses {
				entry, err := data.Files.Mkdir(ctx, at.Path, opts.parents)

				if err != nil {
					return err
				}

				created = append(created, entry)
			}

			emit(out, created, func() []string { return nil })

			return nil
		},
	}

	completion.Args(cmd, completion.Arg{Name: "paths", Variadic: true, Complete: Paths})
	shared.Switch(cmd, &opts.parents, "-p, --parents", "create missing parent folders")
	shared.Option(cmd, &opts.profile, "--profile <name>", "", "profile to use")
	completion.Flag(cmd, "profile", shared.Profiles)

	return cmd
}

func newRmCommand() *cobra.Command {
	var opts struct {
		recursive bool
		yes       bool
		profile   string
	}

	cmd := &cobra.Command{
		Use:   "rm <paths...>",
		Short: "Delete files or folders in a volume",
		Args:  shared.Arguments,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := shared.NewContext(cmd)
			ctx := cmd.Context()
			addresses, err := volumeAddresses(args)

			if err != nil {
				return err
			}

			for _, at := range addresses {
				if at.Path == "" {
					return errs.New("Refusing to delete the volume root. Delete the volume itself with volume delete.")
				}
			}

			data, err := filesFor(ctx, volumesOf(addresses), opts.profile, acrossOneVolume)

			if err != nil {
				return err
			}

			labels := make([]string, len(addresses))

			for index, at := range addresses {
				labels[index] = shown(data.VolumeName, at.Path)
			}

			label := strings.Join(labels, ", ")

			// Only a person at a terminal can answer the prompt.
			if !opts.yes && !isTerminal(out.Stdin) {
				return errs.New("Refusing to delete " + label + " without --yes.")
			}

			if err := shared.RequireDeletion(out, label, opts.yes); err != nil {
				return err
			}

			for _, at := range addresses {
				err := data.Files.Remove(ctx, at.Path, opts.recursive)

				if refusalCode(err) == "directory_not_empty" {
					return errs.New(shown(data.VolumeName, at.Path) + " is a folder that is not empty. Pass -r to delete it and everything in it.")
				}

				if err != nil {
					return err
				}
			}

			return nil
		},
	}

	completion.Args(cmd, completion.Arg{Name: "paths", Variadic: true, Complete: Paths})
	shared.Switch(cmd, &opts.recursive, "-r, --recursive", "delete folders and everything in them")
	shared.Switch(cmd, &opts.yes, "--yes", "skip confirmation")
	shared.Option(cmd, &opts.profile, "--profile <name>", "", "profile to use")
	completion.Flag(cmd, "profile", shared.Profiles)

	return cmd
}

// transferPlan runs a cp, or a mv, that goes through this computer or between volumes.
func transferPlan(ctx context.Context, out *output.Context, route plan, profile string, options copyOptions) error {
	var report transferReport
	var err error

	switch route.Kind {
	case planWithin:
		source, sourceErr := filesFor(ctx, volumesOf(route.VolumeSources), profile, sourcesInOne)

		if sourceErr != nil {
			return sourceErr
		}

		target, targetErr := filesFor(ctx, []string{route.VolumeDestination.Volume}, profile, acrossOneVolume)

		if targetErr != nil {
			return targetErr
		}

		report, err = between(ctx, out, source, route.VolumeSources, target, route.VolumeDestination, options)
	case planUpload:
		target, targetErr := filesFor(ctx, []string{route.VolumeDestination.Volume}, profile, acrossOneVolume)

		if targetErr != nil {
			return targetErr
		}

		report, err = upload(ctx, out, target, route.LocalSources, route.VolumeDestination, options)
	default:
		source, sourceErr := filesFor(ctx, volumesOf(route.VolumeSources), profile, acrossOneVolume)

		if sourceErr != nil {
			return sourceErr
		}

		report, err = download(ctx, out, source, route.VolumeSources, route.LocalDestination, options)
	}

	if err != nil {
		return err
	}

	return finish(out, report, options.DryRun)
}

func newCpCommand() *cobra.Command {
	var opts struct {
		recursive   bool
		overwrite   bool
		update      bool
		concurrency string
		dryRun      bool
		verbose     bool
		progress    bool
		profile     string
	}

	cmd := &cobra.Command{
		Use:   "cp <paths...>",
		Short: "Copy files or folders between this computer and a volume, or between volumes; interrupted copies resume when run again",
		Args:  shared.Arguments,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := shared.NewContext(cmd)
			route, err := direction(args)

			if err != nil {
				return err
			}

			limit, err := concurrency(opts.concurrency)

			if err != nil {
				return err
			}

			options := copyOptions{
				Limit: limit, Recursive: opts.recursive, Overwrite: opts.overwrite, Update: opts.update, DryRun: opts.dryRun, Verbose: opts.verbose, Progress: opts.progress,
			}

			return transferPlan(cmd.Context(), out, route, opts.profile, options)
		},
	}

	completion.Args(cmd, completion.Arg{Name: "paths", Variadic: true, Complete: Paths})
	shared.Switch(cmd, &opts.recursive, "-r, --recursive", "copy folders and everything in them")
	shared.Switch(cmd, &opts.overwrite, "--overwrite", "replace files that already exist at the destination")
	shared.Switch(cmd, &opts.update, "--update", "skip files whose size and modification time already match")
	shared.Option(cmd, &opts.concurrency, "--concurrency <n>", "16", "files at a time, 1 to 32")
	shared.Switch(cmd, &opts.dryRun, "--dry-run", "show what would be copied")
	shared.Switch(cmd, &opts.verbose, "-v, --verbose", "print each file as it is copied")
	shared.Switch(cmd, &opts.progress, "--progress", "show a running total on stderr")
	shared.Option(cmd, &opts.profile, "--profile <name>", "", "profile to use")
	completion.Flag(cmd, "profile", shared.Profiles)
	shared.HelpAfter(cmd, "\nExamples:\n  featherless-cloud volume cp -r ./weights vol://datasets/models/\n  featherless-cloud volume cp vol://datasets/models/out.bin .\n  featherless-cloud volume cp -r vol://datasets/models vol://checkpoints/")

	return cmd
}

func newMvCommand() *cobra.Command {
	var opts struct {
		overwrite   bool
		concurrency string
		verbose     bool
		progress    bool
		profile     string
	}

	cmd := &cobra.Command{
		Use:   "mv <paths...>",
		Short: "Move files or folders inside a volume, between volumes, or between this computer and a volume",
		Args:  shared.Arguments,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := shared.NewContext(cmd)
			ctx := cmd.Context()
			route, err := direction(args)

			if err != nil {
				return err
			}

			if route.Kind != planUpload {
				for _, source := range route.VolumeSources {
					if source.Path == "" {
						return errs.New("Refusing to move a volume root. Name the files or folders in it.")
					}
				}
			}

			limit, err := concurrency(opts.concurrency)

			if err != nil {
				return err
			}

			options := copyOptions{Overwrite: opts.overwrite, Limit: limit, Move: true, Verbose: opts.verbose, Progress: opts.progress}

			if route.Kind != planWithin {
				return transferPlan(ctx, out, route, opts.profile, options)
			}

			from, err := filesFor(ctx, volumesOf(route.VolumeSources), opts.profile, sourcesInOne)

			if err != nil {
				return err
			}

			into, err := filesFor(ctx, []string{route.VolumeDestination.Volume}, opts.profile, acrossOneVolume)

			if err != nil {
				return err
			}

			// Each volume is separate storage: another volume gets a copy, then the source is deleted.
			if into.Files.VolumeID() != from.Files.VolumeID() {
				report, err := between(ctx, out, from, route.VolumeSources, into, route.VolumeDestination, options)

				if err != nil {
					return err
				}

				return finish(out, report, false)
			}

			return moveWithin(ctx, out, from, route, opts.overwrite, opts.verbose)
		},
	}

	completion.Args(cmd, completion.Arg{Name: "paths", Variadic: true, Complete: Paths})
	shared.Switch(cmd, &opts.overwrite, "--overwrite", "replace files at the destination")
	shared.Option(cmd, &opts.concurrency, "--concurrency <n>", "16", "files at a time, 1 to 32")
	shared.Switch(cmd, &opts.verbose, "-v, --verbose", "print each file as it is moved")
	shared.Switch(cmd, &opts.progress, "--progress", "show a running total on stderr")
	shared.Option(cmd, &opts.profile, "--profile <name>", "", "profile to use")
	completion.Flag(cmd, "profile", shared.Profiles)

	return cmd
}

// moveWithin moves files and folders inside one volume on the file server.
func moveWithin(ctx context.Context, out *output.Context, data volumeFiles, route plan, overwrite, verbose bool) error {
	destination := route.VolumeDestination
	intoFolder, err := intoFolderAt(ctx, data.Files, destination)

	if err != nil {
		return err
	}

	if err := requireFolder(intoFolder, len(route.VolumeSources), shown(data.VolumeName, destination.Path)); err != nil {
		return err
	}

	moved := []files.FileEntry{}

	for _, source := range route.VolumeSources {
		target := joinPath(destination.Path, under(intoFolder, lastPart(source.Path), ""))
		entry, err := data.Files.Move(ctx, source.Path, target, overwrite)

		if err != nil {
			return err
		}

		moved = append(moved, entry)
	}

	emit(out, moved, func() []string {
		if !verbose {
			return nil
		}

		lines := make([]string, len(moved))

		for index, item := range moved {
			lines[index] = shown(data.VolumeName, route.VolumeSources[index].Path) + " -> " + shown(data.VolumeName, item.Path)
		}

		return lines
	})

	return nil
}
