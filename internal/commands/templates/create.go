package templates

import (
	"github.com/spf13/cobra"

	"featherless.ai/featherless-cloud/internal/api"
	"featherless.ai/featherless-cloud/internal/commands/shared"
	"featherless.ai/featherless-cloud/internal/completion"
	"featherless.ai/featherless-cloud/internal/errs"
	"featherless.ai/featherless-cloud/internal/output"
)

func shortIDText(template api.Template) func() output.Result {
	return func() output.Result { return output.Result{Text: template.ShortID} }
}

func newCreateCommand() *cobra.Command {
	var flags templateFlags
	var opts struct {
		profile string
	}

	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Save launch settings as a template, private unless --visibility organization",
		Args:  shared.Arguments,
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]

			if err := shared.AssertInstanceName(name); err != nil {
				return err
			}

			if flags.template == "" && flags.image == "" {
				return errs.New("Pass --image, or --template to start from an official template.")
			}

			ctx := shared.NewContext(cmd)

			client, err := shared.Client(opts.profile)
			if err != nil {
				return err
			}

			body, err := templateBody(client, flags, cmd, ctx)
			if err != nil {
				return err
			}

			body.Name = &name

			template, err := output.Spin(ctx, "Saving template", func() (api.Template, error) {
				return client.CreateTemplate(cmd.Context(), body)
			})

			if err != nil {
				return err
			}

			ctx.Success("Saved template " + template.Name + ". Launch it with featherless-cloud instance create --template " + template.Name + ".")
			ctx.Output(template, shortIDText(template))

			return nil
		},
	}

	completion.Args(cmd, completion.Arg{Name: "name", Description: "lowercase letters, numbers and hyphens; what you launch it by"})
	withTemplateFlags(cmd, &flags)
	shared.Option(cmd, &opts.profile, "--profile <name>", "", "profile to use")
	completion.Flag(cmd, "profile", shared.Profiles)
	shared.HelpAfter(cmd, "\nExamples:\n  featherless-cloud templates create trainer --image ghcr.io/acme/trainer:1 --gpu-count 1 --env EPOCHS=3\n  featherless-cloud instance create --template trainer")

	return cmd
}
