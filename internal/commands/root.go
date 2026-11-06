// Package commands builds the featherless-cloud command tree.
package commands

import (
	"fmt"

	"github.com/spf13/cobra"

	"featherless.ai/featherless-cloud/internal/commands/billing"
	"featherless.ai/featherless-cloud/internal/commands/instance"
	"featherless.ai/featherless-cloud/internal/commands/job"
	"featherless.ai/featherless-cloud/internal/commands/pricing"
	"featherless.ai/featherless-cloud/internal/commands/registrykeys"
	"featherless.ai/featherless-cloud/internal/commands/sshkeys"
	"featherless.ai/featherless-cloud/internal/commands/templates"
	"featherless.ai/featherless-cloud/internal/commands/volume"
	"featherless.ai/featherless-cloud/internal/errs"
	"featherless.ai/featherless-cloud/internal/update"
	"featherless.ai/featherless-cloud/internal/version"
)

// NewRoot builds the whole command tree, fresh on every call.
func NewRoot() *cobra.Command {
	cobra.EnableCommandSorting = false

	root := &cobra.Command{
		Use:               version.AppName,
		Short:             "Featherless GPU Cloud CLI",
		SilenceErrors:     true,
		SilenceUsage:      true,
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
		// --version is a program option, so it also works after a subcommand.
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if !versionRequested(cmd) {
				return nil
			}

			fmt.Fprintln(cmd.OutOrStdout(), version.Version)
			return &errs.Exit{Code: 0}
		},
	}

	flags := root.PersistentFlags()
	flags.SortFlags = false
	flags.Bool("json", false, "output as JSON (for scripts and agents)")
	flags.Bool("non-interactive", false, "disable spinners and prompts")
	flags.BoolP("version", "V", false, "output the version number")

	// Commands without an action (the root and groups) show help instead of
	// running the pre-run hook.
	showHelp := root.HelpFunc()
	root.SetHelpFunc(func(cmd *cobra.Command, args []string) {
		if versionRequested(cmd) {
			fmt.Fprintln(cmd.OutOrStdout(), version.Version)
			return
		}

		showHelp(cmd, args)
	})

	root.AddCommand(
		newLoginCommand(),
		newLogoutCommand(),
		instance.Command(),
		volume.Command(),
		pricing.Command(),
		billing.Command(),
		job.Command(),
		newExecCommand(),
		sshkeys.Command(),
		registrykeys.Command(),
		templates.Command(),
		newUpgradeCommand(),
		newCompletionCommand(),
		newCompleteCommand(),
		newCheckUpdateCommand(),
	)

	return root
}

func versionRequested(cmd *cobra.Command) bool {
	requested, err := cmd.Flags().GetBool("version")
	return err == nil && requested
}

func newCheckUpdateCommand() *cobra.Command {
	return &cobra.Command{
		Use:    "_check-update",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			update.RunUpdateCheck(cmd.Context())
			return nil
		},
	}
}
