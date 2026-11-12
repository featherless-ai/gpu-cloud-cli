// Package instance is the `instance` command group.
package instance

import (
	"github.com/spf13/cobra"

	"featherless.ai/featherless-cloud/internal/api"
)

func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "instance",
		Short: "Manage GPU instances",
	}

	cmd.AddCommand(
		newListCommand(),
		newGetCommand(),
		newCreateCommand(),
		newUpdateCommand(),
		newActionCommand(api.ActionStart),
		newActionCommand(api.ActionStop),
		newActionCommand(api.ActionRestart),
		newActionCommand(api.ActionDelete),
		newLogsCommand(),
		newHistoryCommand(),
		newShellCommand(),
		newSSHCommand(),
		newTmuxCommand(),
	)

	return cmd
}
