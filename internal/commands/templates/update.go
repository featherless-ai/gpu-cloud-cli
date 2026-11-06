package templates

import (
	"reflect"

	"github.com/spf13/cobra"

	"featherless.ai/featherless-cloud/internal/api"
	"featherless.ai/featherless-cloud/internal/commands/shared"
	"featherless.ai/featherless-cloud/internal/completion"
	"featherless.ai/featherless-cloud/internal/errs"
	"featherless.ai/featherless-cloud/internal/output"
)

func findTemplate(cmd *cobra.Command, ctx *output.Context, client api.Cloud, ref string) (api.Template, error) {
	templates, err := output.Spin(ctx, "Finding template", func() ([]api.Template, error) {
		return client.Templates(cmd.Context())
	})

	if err != nil {
		return api.Template{}, err
	}

	return shared.ResolveTemplateRef(templates, ref)
}

func newUpdateCommand() *cobra.Command {
	var flags templateFlags
	var opts struct {
		name    string
		profile string
	}

	cmd := &cobra.Command{
		Use:   "update <template>",
		Short: "Change a template; instances and Run Jobs already launched keep their settings",
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

			body, err := templateBody(client, flags, cmd, ctx)
			if err != nil {
				return err
			}

			if opts.name != "" {
				if err := shared.AssertInstanceName(opts.name); err != nil {
					return err
				}

				body.Name = &opts.name
			}

			if reflect.DeepEqual(body, api.TemplateBody{}) {
				return errs.New("Nothing to change. Pass the settings to change, as for templates create.")
			}

			updated, err := output.Spin(ctx, "Updating template", func() (api.Template, error) {
				return client.UpdateTemplate(cmd.Context(), template.ID, body)
			})

			if err != nil {
				return err
			}

			ctx.Success("Updated template " + updated.Name + ".")
			ctx.Output(updated, shortIDText(updated))

			return nil
		},
	}

	completion.Args(cmd, completion.Arg{Name: "template", Complete: shared.Templates})
	shared.Option(cmd, &opts.name, "--name <name>", "", "new name")
	withTemplateFlags(cmd, &flags)
	shared.Option(cmd, &opts.profile, "--profile <name>", "", "profile to use")
	completion.Flag(cmd, "profile", shared.Profiles)

	return cmd
}
