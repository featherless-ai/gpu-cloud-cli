// Package billing is the `billing` command group.
package billing

import "github.com/spf13/cobra"

func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "billing",
		Short: "Show balance and usage history",
	}

	cmd.AddCommand(
		newSettingsCommand(),
		newHistoryCommand(),
	)

	return cmd
}
