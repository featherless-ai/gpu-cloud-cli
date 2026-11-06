package job

import (
	"github.com/spf13/cobra"

	"featherless.ai/featherless-cloud/internal/commands/shared"
	"featherless.ai/featherless-cloud/internal/completion"
	"featherless.ai/featherless-cloud/internal/errs"
)

func newCancelCommand() *cobra.Command {
	var opts struct {
		profile string
	}

	cmd := &cobra.Command{
		Use:   "cancel <id>",
		Short: "Cancel a queued or running Run Job",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return errs.New("not implemented")
		},
	}

	completion.Args(cmd, completion.Arg{Name: "id", Complete: shared.Jobs})
	shared.Option(cmd, &opts.profile, "--profile <name>", "", "profile to use")
	completion.Flag(cmd, "profile", shared.Profiles)

	return cmd
}

func newDeleteCommand() *cobra.Command {
	var opts struct {
		yes     bool
		profile string
	}

	cmd := &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a Run Job record",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return errs.New("not implemented")
		},
	}

	completion.Args(cmd, completion.Arg{Name: "id", Complete: shared.Jobs})
	shared.Switch(cmd, &opts.yes, "--yes", "skip confirmation")
	shared.Option(cmd, &opts.profile, "--profile <name>", "", "profile to use")
	completion.Flag(cmd, "profile", shared.Profiles)

	return cmd
}
