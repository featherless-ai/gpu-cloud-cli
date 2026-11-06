package commands

import (
	"github.com/spf13/cobra"

	"featherless.ai/featherless-cloud/internal/commands/shared"
	"featherless.ai/featherless-cloud/internal/completion"
	"featherless.ai/featherless-cloud/internal/errs"
)

func newLoginCommand() *cobra.Command {
	var opts struct {
		profile string
		key     string
	}

	cmd := &cobra.Command{
		Use:   "login",
		Short: "Store an API key for this device",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return errs.New("not implemented")
		},
	}

	shared.Option(cmd, &opts.profile, "--profile <name>", "default", "profile to store the credentials under")
	completion.Flag(cmd, "profile", shared.Profiles)
	shared.Option(cmd, &opts.key, "--key <secret>", "", "API key to store (non-interactive)")

	return cmd
}
