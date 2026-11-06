package registrykeys

import (
	"github.com/spf13/cobra"

	"featherless.ai/featherless-cloud/internal/commands/shared"
	"featherless.ai/featherless-cloud/internal/completion"
	"featherless.ai/featherless-cloud/internal/errs"
)

func newUpdateCommand() *cobra.Command {
	var opts struct {
		name           string
		server         string
		username       string
		changePassword bool
		passwordStdin  bool
		profile        string
	}

	cmd := &cobra.Command{
		Use:   "update <key>",
		Short: "Change a registry key",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return errs.New("not implemented")
		},
	}

	completion.Args(cmd, completion.Arg{Name: "key", Complete: shared.Registries})
	shared.Option(cmd, &opts.name, "--name <name>", "", "new name")
	shared.Option(cmd, &opts.server, "--server <host>", "", "new registry host")
	shared.Option(cmd, &opts.username, "--username <user>", "", "new registry username")
	shared.Switch(cmd, &opts.changePassword, "--change-password", "prompt for a new password or access token")
	shared.Switch(cmd, &opts.passwordStdin, "--password-stdin", "read a new password or access token from stdin")
	shared.Option(cmd, &opts.profile, "--profile <name>", "", "profile to use")
	completion.Flag(cmd, "profile", shared.Profiles)

	return cmd
}
