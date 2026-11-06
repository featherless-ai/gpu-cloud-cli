// Package volume is the `volume` command group: network volumes and their files.
package volume

import "github.com/spf13/cobra"

func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "volume",
		Short: "Manage network storage volumes",
	}

	cmd.AddCommand(
		newGetCommand(),
		newCreateCommand(),
		newUpdateCommand(),
		newDeleteCommand(),
		newLsCommand(),
		newFindCommand(),
		newMkdirCommand(),
		newRmCommand(),
		newCpCommand(),
		newMvCommand(),
		newSyncCommand(),
	)

	return cmd
}
