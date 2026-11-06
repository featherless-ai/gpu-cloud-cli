package volume

import (
	"context"
	"regexp"

	"github.com/spf13/cobra"

	"featherless.ai/featherless-cloud/internal/commands/shared"
	"featherless.ai/featherless-cloud/internal/completion"
	"featherless.ai/featherless-cloud/internal/errs"
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
	return nil, errs.New("not implemented")
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
	return syncReport{}, errs.New("not implemented")
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
		Use:   "sync <local-folder> <vol://<volume>/<path>",
		Short: "Sync a folder with a folder in a volume, sending what changed each way; never deletes",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return errs.New("not implemented")
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
