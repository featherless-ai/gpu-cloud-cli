package instance

import (
	"github.com/spf13/cobra"

	"featherless.ai/featherless-cloud/internal/commands/shared"
	"featherless.ai/featherless-cloud/internal/completion"
	"featherless.ai/featherless-cloud/internal/errs"
)

// NewContent is the part of next not already printed from the previous tail.
func NewContent(previous, next string) string {
	return "not implemented"
}

// NextLogCursor keeps the overlap state while live logs hand off to the final
// retained logs.
func NextLogCursor(previous, next string, complete bool) string {
	return "not implemented"
}

func newLogsCommand() *cobra.Command {
	var opts struct {
		head    bool
		tail    string
		follow  bool
		profile string
	}

	cmd := &cobra.Command{
		Use:   "logs <id>",
		Short: "Print bounded debug output; use your own logging service for complete logs",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return errs.New("not implemented")
		},
	}

	completion.Args(cmd, completion.Arg{Name: "id", Complete: shared.Instances})
	shared.Switch(cmd, &opts.head, "--head", "show the captured beginning instead of recent output")
	shared.Option(cmd, &opts.tail, "--tail <n>", "500", "lines of history (1-5000)")
	shared.Switch(cmd, &opts.follow, "--follow", "keep streaming new lines")
	shared.Option(cmd, &opts.profile, "--profile <name>", "", "profile to use")
	completion.Flag(cmd, "profile", shared.Profiles)

	return cmd
}
