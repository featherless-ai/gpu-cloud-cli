package volume

import (
	"strings"

	"github.com/spf13/cobra"

	"featherless.ai/featherless-cloud/internal/api"
	"featherless.ai/featherless-cloud/internal/commands/shared"
	"featherless.ai/featherless-cloud/internal/completion"
	"featherless.ai/featherless-cloud/internal/output"
	"featherless.ai/featherless-cloud/internal/version"
)

func newGetCommand() *cobra.Command {
	var opts struct {
		profile string
	}

	cmd := &cobra.Command{
		Use:   "get <id>",
		Short: "Show one network volume",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := shared.NewContext(cmd)
			ctx := cmd.Context()
			client, err := shared.Client(opts.profile)

			if err != nil {
				return err
			}

			volume, err := output.Spin(out, "Fetching volume", func() (api.NetworkVolume, error) {
				return findAndGetVolume(ctx, client, args[0])
			})

			if err != nil {
				return err
			}

			if volume.LastError != nil && *volume.LastError != "" {
				out.Warn(*volume.LastError)
			}

			out.Output(volume, func() output.Result {
				lines := []string{
					"NAME        " + volume.Name,
					"ID          " + volume.ID,
					"STATE       " + string(volume.State),
					"CAPACITY    " + itoa(volume.CapacityGiB) + " GiB",
					"PRICE       " + version.FormatDollars(volume.Pricing.UnitPriceMicrosPerGiBMonth) + " / GiB / month",
					"EST/MONTH   " + version.FormatDollars(volume.Pricing.EstimatedMicrosPerMonth) + " / month",
				}

				for _, attachment := range volume.Attachments {
					lines = append(lines, "ATTACHED TO "+attachment.Name+" ("+attachment.ShortID+") → "+attachment.MountPath)
				}

				lines = append(lines, "CREATED     "+volume.CreatedAt[:min(10, len(volume.CreatedAt))])

				return output.Result{Text: strings.Join(lines, "\n")}
			})

			return nil
		},
	}

	completion.Args(cmd, completion.Arg{Name: "id", Complete: shared.Volumes})
	shared.Option(cmd, &opts.profile, "--profile <name>", "", "profile to use")
	completion.Flag(cmd, "profile", shared.Profiles)

	return cmd
}
