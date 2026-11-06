package volume

import (
	"time"

	"github.com/spf13/cobra"

	"featherless.ai/featherless-cloud/internal/commands/shared"
	"featherless.ai/featherless-cloud/internal/completion"
	"featherless.ai/featherless-cloud/internal/errs"
)

const (
	waitPoll     = time.Second
	waitMaxPolls = 30
)

// Seam tests replace: how --wait pauses between polls.
var sleep = time.Sleep

func parseCapacity(value string) (int, error) {
	return 0, errs.New("not implemented")
}

func newCreateCommand() *cobra.Command {
	var opts struct {
		gb      string
		wait    bool
		profile string
	}

	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a network volume",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return errs.New("not implemented")
		},
	}

	completion.Args(cmd, completion.Arg{Name: "name"})
	shared.Option(cmd, &opts.gb, "--gb <n>", "100", "capacity in GB (10-10240)")
	shared.Switch(cmd, &opts.wait, "--wait", "wait until the volume is usable")
	shared.Option(cmd, &opts.profile, "--profile <name>", "", "profile to use")
	completion.Flag(cmd, "profile", shared.Profiles)

	return cmd
}
