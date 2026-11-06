package templates

import (
	"github.com/spf13/cobra"

	"featherless.ai/featherless-cloud/internal/commands/shared"
	"featherless.ai/featherless-cloud/internal/completion"
	"featherless.ai/featherless-cloud/internal/errs"
)

func newCreateCommand() *cobra.Command {
	var flags templateFlags
	var opts struct {
		profile string
	}

	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Save launch settings as a template, private unless --visibility organization",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return errs.New("not implemented")
		},
	}

	completion.Args(cmd, completion.Arg{Name: "name"})
	withTemplateFlags(cmd, &flags)
	shared.Option(cmd, &opts.profile, "--profile <name>", "", "profile to use")
	completion.Flag(cmd, "profile", shared.Profiles)
	shared.HelpAfter(cmd, "\nExamples:\n  featherless-cloud templates create trainer --image ghcr.io/acme/trainer:1 --gpu-count 1 --env EPOCHS=3\n  featherless-cloud instance create --template trainer")

	return cmd
}
