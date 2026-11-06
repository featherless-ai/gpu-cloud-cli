package volume

import (
	"context"
	"os"
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
	schemePrefix = 3
	addressHint  = "Write volume paths as vol://<volume>/<path>."
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

// remotePath is a volume path as people type it: leading and trailing slashes are dropped.
func remotePath(value string) (string, error) {
	return "", errs.New("not implemented")
}

// Paths completes `vol` → `vol://`, then volume names, then folder entries;
// nothing for local paths, so the shell completes files.
func Paths(word string, ctx *completion.Context) ([]string, error) {
	return nil, errs.New("not implemented")
}

type address struct {
	Volume string
	Path   string
	// Folder is the volume root, or a path written with a trailing slash.
	Folder bool
}

// parseAddress reads `vol://<volume>/<path>`; nil for a path on this computer.
func parseAddress(value string) (*address, error) {
	return nil, errs.New("not implemented")
}

func volumeAddress(value string) (address, error) {
	return address{}, errs.New("not implemented")
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
	return plan{}, errs.New("not implemented")
}

// under is a copied file's path below the destination, by cp's rules: into a
// folder each source keeps its own name, otherwise the one source takes the
// destination's. A volume root has no name, so its contents are copied.
func under(intoFolder bool, sourceName, rest string) string {
	return ""
}

func formatSize(bytes float64) string {
	return ""
}

// parseSizes reads `--size` bounds as find writes them: +N for more than N, -N
// for less, in bytes or K, M, G and T. Only the size fields are set.
func parseSizes(values []string) (files.FindFilters, error) {
	return files.FindFilters{}, errs.New("not implemented")
}

// parseAge reads an age like 30m, 12h or 7d.
func parseAge(value string) (time.Duration, error) {
	return 0, errs.New("not implemented")
}

type listingLine struct {
	At string
	// Bytes is nil for folders.
	Bytes *int64
	Name  string
}

// listing renders lines like `aws s3 ls`: local time, size (blank for folders), name.
func listing(lines []listingLine, human bool) []string {
	return nil
}

type count struct {
	Files int
	Bytes int64
}

func progressLine(verb string, done, total count, elapsed time.Duration) string {
	return ""
}

type volumeFiles struct {
	Files      *files.Client
	VolumeName string
}

// filesFor is one file client for the volume every reference names.
func filesFor(ctx context.Context, refs []string, profile, acrossVolumes string) (volumeFiles, error) {
	return volumeFiles{}, errs.New("not implemented")
}

func concurrency(value string) (int, error) {
	return 0, errs.New("not implemented")
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

// sameModifiedTime tells whether a local file and a volume file carry the same
// modification time. The volume keeps whole milliseconds, and copies keep the
// local time cut to the millisecond, so a copy always matches.
func sameModifiedTime(local time.Time, modifiedAt string) bool {
	return false
}

// upload copies files from this computer into a volume.
func upload(ctx context.Context, out *output.Context, data volumeFiles, sources []string, destination address, options copyOptions) (transferReport, error) {
	return transferReport{}, errs.New("not implemented")
}

// download copies files from a volume to this computer.
func download(ctx context.Context, out *output.Context, data volumeFiles, sources []address, destination string, options copyOptions) (transferReport, error) {
	return transferReport{}, errs.New("not implemented")
}

// between copies or moves files from one volume into another on the file
// server; nothing passes through this computer.
func between(ctx context.Context, out *output.Context, source volumeFiles, sourcePaths []address, target volumeFiles, destination address, options copyOptions) (transferReport, error) {
	return transferReport{}, errs.New("not implemented")
}

type findOptions struct {
	Long   bool
	Human  bool
	Print0 bool
}

// find prints each match as a vol:// address as the file server finds it, or,
// with Long or --json, every match once the search finished.
func find(ctx context.Context, out *output.Context, data volumeFiles, at address, filters files.FindFilters, options findOptions) error {
	return errs.New("not implemented")
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
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return errs.New("not implemented")
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

	return cmd
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
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return errs.New("not implemented")
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

	return cmd
}

func newMkdirCommand() *cobra.Command {
	var opts struct {
		parents bool
		profile string
	}

	cmd := &cobra.Command{
		Use:   "mkdir <paths...>",
		Short: "Create folders in a volume",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return errs.New("not implemented")
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
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return errs.New("not implemented")
		},
	}

	completion.Args(cmd, completion.Arg{Name: "paths", Variadic: true, Complete: Paths})
	shared.Switch(cmd, &opts.recursive, "-r, --recursive", "delete folders and everything in them")
	shared.Switch(cmd, &opts.yes, "--yes", "skip confirmation")
	shared.Option(cmd, &opts.profile, "--profile <name>", "", "profile to use")
	completion.Flag(cmd, "profile", shared.Profiles)

	return cmd
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
		Use:   "cp <sources...> <destination>",
		Short: "Copy files or folders between this computer and a volume, or between volumes; interrupted copies resume when run again",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return errs.New("not implemented")
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
		Use:   "mv <sources...> <destination>",
		Short: "Move files or folders inside a volume, between volumes, or between this computer and a volume",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return errs.New("not implemented")
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
