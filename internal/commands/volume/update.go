package volume

import (
	"github.com/spf13/cobra"

	"featherless.ai/featherless-cloud/internal/commands/shared"
	"featherless.ai/featherless-cloud/internal/completion"
	"featherless.ai/featherless-cloud/internal/errs"
)

func newUpdateCommand() *cobra.Command {
	var opts struct {
		name    string
		gb      string
		profile string
	}

	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Rename or grow a network volume",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return errs.New("not implemented")
		},
	}

	completion.Args(cmd, completion.Arg{Name: "id", Complete: shared.Volumes})
	shared.Option(cmd, &opts.name, "--name <new>", "", "new volume name")
	shared.Option(cmd, &opts.gb, "--gb <n>", "", "new capacity in GB (grow-only, 10-10240)")
	shared.Option(cmd, &opts.profile, "--profile <name>", "", "profile to use")
	completion.Flag(cmd, "profile", shared.Profiles)

	return cmd
}
