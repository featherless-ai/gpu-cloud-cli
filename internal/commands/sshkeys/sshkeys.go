// Package sshkeys is the `ssh-keys` command group.
package sshkeys

import "github.com/spf13/cobra"

func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ssh-keys",
		Short: "Manage your SSH public keys",
	}

	cmd.AddCommand(
		newListCommand(),
		newAddCommand(),
		newDeleteCommand(),
	)

	return cmd
}
