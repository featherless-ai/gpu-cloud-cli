// Package registrykeys is the `registry-keys` command group.
package registrykeys

import "github.com/spf13/cobra"

func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "registry-keys",
		Short: "Manage container registry logins for private images",
	}

	cmd.AddCommand(
		newListCommand(),
		newAddCommand(),
		newUpdateCommand(),
		newDeleteCommand(),
	)

	return cmd
}
