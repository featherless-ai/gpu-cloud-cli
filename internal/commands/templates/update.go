package templates

import (
	"github.com/spf13/cobra"

	"featherless.ai/featherless-cloud/internal/commands/shared"
	"featherless.ai/featherless-cloud/internal/completion"
	"featherless.ai/featherless-cloud/internal/errs"
)

func newUpdateCommand() *cobra.Command {
	var flags templateFlags
	var opts struct {
		name    string
		profile string
	}

	cmd := &cobra.Command{
		Use:   "update <template>",
		Short: "Change a template; instances and Run Jobs already launched keep their settings",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return errs.New("not implemented")
		},
	}

	completion.Args(cmd, completion.Arg{Name: "template", Complete: shared.Templates})
	shared.Option(cmd, &opts.name, "--name <name>", "", "new name")
	withTemplateFlags(cmd, &flags)
	shared.Option(cmd, &opts.profile, "--profile <name>", "", "profile to use")
	completion.Flag(cmd, "profile", shared.Profiles)

	return cmd
}
