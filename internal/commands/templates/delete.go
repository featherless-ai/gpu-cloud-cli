package templates

import (
	"github.com/spf13/cobra"

	"featherless.ai/featherless-cloud/internal/commands/shared"
	"featherless.ai/featherless-cloud/internal/completion"
	"featherless.ai/featherless-cloud/internal/output"
)

func newDeleteCommand() *cobra.Command {
	var opts struct {
		yes     bool
		profile string
	}

	cmd := &cobra.Command{
		Use:   "delete <template>",
		Short: "Delete a template; instances and Run Jobs launched from it keep running",
		Args:  shared.Arguments,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := shared.NewContext(cmd)

			client, err := shared.Client(opts.profile)
			if err != nil {
				return err
			}

			template, err := findTemplate(cmd, ctx, client, args[0])
			if err != nil {
				return err
			}

			if err := shared.RequireDeletion(ctx, "template "+template.Name, opts.yes); err != nil {
				return err
			}

			_, err = output.Spin(ctx, "Deleting template", func() (int, error) {
				return client.DeleteTemplate(cmd.Context(), template.ID)
			})

			if err != nil {
				return err
			}

			ctx.Success("Deleted template " + template.Name + ".")

			return nil
		},
	}

	completion.Args(cmd, completion.Arg{Name: "template", Description: "template name or id", Complete: shared.Templates})
	shared.Switch(cmd, &opts.yes, "--yes", "skip confirmation")
	shared.Option(cmd, &opts.profile, "--profile <name>", "", "profile to use")
	completion.Flag(cmd, "profile", shared.Profiles)

	return cmd
}
