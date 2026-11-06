package commands

import (
	"github.com/spf13/cobra"

	"featherless.ai/featherless-cloud/internal/commands/shared"
	"featherless.ai/featherless-cloud/internal/errs"
)

func newUpgradeCommand() *cobra.Command {
	var opts struct {
		force bool
	}

	cmd := &cobra.Command{
		Use:   "upgrade",
		Short: "Upgrade featherless-cloud to the latest published version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return errs.New("not implemented")
		},
	}

	shared.Switch(cmd, &opts.force, "--force", "reinstall even when already up to date")

	return cmd
}
