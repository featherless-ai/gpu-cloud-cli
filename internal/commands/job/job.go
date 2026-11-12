// Package job is the `runjob` (alias `job`) command group.
package job

import "github.com/spf13/cobra"

func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "runjob",
		Aliases: []string{"job"},
		Short:   "Run and manage Run Jobs",
	}

	cmd.AddCommand(
		newListCommand(),
		newGetCommand(),
		newLogsCommand(),
		newCancelCommand(),
		newDeleteCommand(),
	)

	return cmd
}
