// Package pricing is the `pricing` command group.
package pricing

import "github.com/spf13/cobra"

func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pricing",
		Short: "Browse compute and storage prices",
	}

	cmd.AddCommand(newListCommand())

	return cmd
}
