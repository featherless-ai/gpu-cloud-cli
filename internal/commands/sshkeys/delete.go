package sshkeys

import (
	"github.com/spf13/cobra"

	"featherless.ai/featherless-cloud/internal/commands/shared"
	"featherless.ai/featherless-cloud/internal/completion"
	"featherless.ai/featherless-cloud/internal/errs"
)

func newDeleteCommand() *cobra.Command {
	var opts struct {
		yes     bool
		profile string
	}

	cmd := &cobra.Command{
		Use:   "delete <key>",
		Short: "Remove an SSH public key",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return errs.New("not implemented")
		},
	}

	completion.Args(cmd, completion.Arg{Name: "key", Complete: shared.SSHKeys})
	shared.Switch(cmd, &opts.yes, "--yes", "skip confirmation")
	shared.Option(cmd, &opts.profile, "--profile <name>", "", "profile to use")
	completion.Flag(cmd, "profile", shared.Profiles)

	return cmd
}
