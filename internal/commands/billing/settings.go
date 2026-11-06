package billing

import (
	"github.com/spf13/cobra"

	"featherless.ai/featherless-cloud/internal/api"
	"featherless.ai/featherless-cloud/internal/commands/shared"
	"featherless.ai/featherless-cloud/internal/completion"
	"featherless.ai/featherless-cloud/internal/config"
	"featherless.ai/featherless-cloud/internal/output"
	"featherless.ai/featherless-cloud/internal/version"
)

func newSettingsCommand() *cobra.Command {
	var opts struct {
		profile string
	}

	cmd := &cobra.Command{
		Use:   "settings",
		Short: "Show balance and reload settings",
		Args:  shared.Arguments,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := shared.NewContext(cmd)

			client, err := shared.Client(opts.profile)
			if err != nil {
				return err
			}

			summary, err := output.Spin(ctx, "Fetching billing", func() (api.BillingSummary, error) {
				return client.BillingSummary(cmd.Context())
			})

			if err != nil {
				return err
			}

			ctx.Output(summary, func() output.Result {
				reload := "disabled"

				if summary.AutoReload.Enabled {
					reload = "enabled"
				}

				balance := "Balance       " + version.FormatDollars(summary.BalanceMicros) + " (" + summary.Currency + ")"

				return output.Result{Text: balance + "\nAuto-reload   " + reload}
			})

			ctx.Log("Add credit at " + config.ConsoleBaseURL() + "/billing")

			return nil
		},
	}

	shared.Option(cmd, &opts.profile, "--profile <name>", "", "profile to use")
	completion.Flag(cmd, "profile", shared.Profiles)

	return cmd
}
