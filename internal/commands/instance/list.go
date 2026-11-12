package instance

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"

	"featherless.ai/featherless-cloud/internal/api"
	"featherless.ai/featherless-cloud/internal/commands/shared"
	"featherless.ai/featherless-cloud/internal/completion"
	"featherless.ai/featherless-cloud/internal/output"
	"featherless.ai/featherless-cloud/internal/version"
)

func stateLabel(state api.InstanceState, stage *api.StartupStage) string {
	if stage == nil || *stage == "" {
		return string(state)
	}

	return fmt.Sprintf("%s (%s)", state, *stage)
}

func newListCommand() *cobra.Command {
	var opts struct {
		page    string
		limit   string
		profile string
	}

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List GPU instances",
		Args:  shared.Arguments,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := shared.NewContext(cmd)

			client, err := shared.Client(opts.profile)
			if err != nil {
				return err
			}

			page := int(shared.ParseNumber(opts.page))
			limit := int(shared.ParseNumber(opts.limit))

			data, err := output.Spin(ctx, "Fetching instances", func() (api.InstanceList, error) {
				return client.ListInstances(cmd.Context(), page, limit)
			})

			if err != nil {
				return err
			}

			ctx.Output(data, func() output.Result {
				rows := make([][]string, len(data.Items))

				for i, item := range data.Items {
					gpus := strconv.Itoa(item.GPUCount)

					if item.ComputeType == api.ComputeCPU {
						gpus = "-"
					}

					rows[i] = []string{
						item.Name,
						item.ShortID,
						stateLabel(item.State, item.StartupStage),
						item.ComputeLabel,
						gpus,
						version.FormatDollars(item.CostMicrosPerHour) + "/hr",
						shared.Slice(item.CreatedAt, 10),
					}
				}

				pagination := data.Pagination
				footer := fmt.Sprintf("Page %d of %d (%d total)", pagination.Page, pagination.TotalPages, pagination.TotalItems)
				table := &output.Table{Header: []string{"NAME", "ID", "STATE", "COMPUTE", "GPU", "PRICE/HR", "CREATED"}, Rows: rows}

				return output.Result{Table: table, Footer: footer}
			})

			return nil
		},
	}

	shared.Option(cmd, &opts.page, "--page <number>", "1", "page number")
	shared.Option(cmd, &opts.limit, "--limit <number>", "20", "instances per page (1-100)")
	shared.Option(cmd, &opts.profile, "--profile <name>", "", "profile to use")
	completion.Flag(cmd, "profile", shared.Profiles)

	return cmd
}
