package job

import (
	"github.com/spf13/cobra"

	"featherless.ai/featherless-cloud/internal/commands/shared"
	"featherless.ai/featherless-cloud/internal/completion"
	"featherless.ai/featherless-cloud/internal/errs"
)

// attempts completes the attempts with recorded output of the job named by
// the first argument.
func attempts(word string, ctx *completion.Context) ([]string, error) {
	return nil, errs.New("not implemented")
}

func newLogsCommand() *cobra.Command {
	var opts struct {
		head    bool
		tail    string
		follow  bool
		attempt string
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

	completion.Args(cmd, completion.Arg{Name: "id", Complete: shared.Jobs})
	shared.Switch(cmd, &opts.head, "--head", "show the captured beginning instead of recent output")
	shared.Option(cmd, &opts.tail, "--tail <n>", "500", "lines of history (1-5000)")
	shared.Switch(cmd, &opts.follow, "--follow", "keep streaming until the Run Job ends")
	shared.Option(cmd, &opts.attempt, "--attempt <n>", "", "output of an earlier attempt of a job that was started again")
	completion.Flag(cmd, "attempt", attempts)
	shared.Option(cmd, &opts.profile, "--profile <name>", "", "profile to use")
	completion.Flag(cmd, "profile", shared.Profiles)

	return cmd
}
