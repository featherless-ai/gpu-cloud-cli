package commands

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"featherless.ai/featherless-cloud/internal/commands/shared"
	"featherless.ai/featherless-cloud/internal/completion"
)

func newCompletionCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "completion <shell>",
		Short: "Print a shell completion script (bash, zsh)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			script := completion.ZshScript()

			if args[0] == "bash" {
				script = completion.BashScript()
			}

			fmt.Fprintln(cmd.OutOrStdout(), script)
			return nil
		},
	}

	completion.Args(cmd, completion.Arg{Name: "shell", Choices: []string{"bash", "zsh"}})

	return cmd
}

// newCompleteCommand is what the shell scripts run on every Tab. FC_WORD
// carries the word being typed, which as argv could parse as an option.
func newCompleteCommand() *cobra.Command {
	return &cobra.Command{
		Use:    "_complete",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			completion.Run(cmd.Root(), os.Getenv, shared.Client, cmd.OutOrStdout())
			return nil
		},
	}
}
