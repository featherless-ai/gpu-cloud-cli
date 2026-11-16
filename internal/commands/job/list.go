package job

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

func isWaiting(job api.RunJob) bool {
	return job.AdmissionState != nil && *job.AdmissionState == api.AdmissionWaiting
}

func gpuModelOr(job api.RunJob, fallback string) string {
	if job.Resources.GPUModel != nil {
		return *job.Resources.GPUModel
	}

	return fallback
}

func newListCommand() *cobra.Command {
	var opts struct {
		page    string
		limit   string
		profile string
	}

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List Run Jobs",
		Args:  shared.Arguments,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := shared.NewContext(cmd)

			client, err := shared.Client(opts.profile)
			if err != nil {
				return err
			}

			page := int(shared.ParseNumber(opts.page))
			limit := int(shared.ParseNumber(opts.limit))

			data, err := client.ListJobs(cmd.Context(), page, limit)

			if err != nil {
				return err
			}

			ctx.Output(data, func() output.Result {
				rows := make([][]string, len(data.Items))

				for i, item := range data.Items {
					state := string(item.State)

					if isWaiting(item) {
						state = "waiting"
					}

					compute := fmt.Sprintf("%d vCPU", item.Resources.CPUCores)

					if item.ComputeType == api.ComputeGPU {
						compute = fmt.Sprintf("%d x %s", item.GPUCount, gpuModelOr(item, item.ComputeSKU))
					}

					exit := "-"

					if item.ExitCode != nil {
						exit = strconv.Itoa(*item.ExitCode)
					}

					rows[i] = []string{
						item.Name,
						item.ShortID,
						state,
						compute,
						exit,
						version.FormatDollars(item.CostMicrosPerHour) + "/hr",
						shared.Slice(item.CreatedAt, 10),
					}
				}

				pagination := data.Pagination
				footer := fmt.Sprintf("Page %d of %d (%d total)", pagination.Page, pagination.TotalPages, pagination.TotalItems)
				table := &output.Table{Header: []string{"NAME", "ID", "STATE", "COMPUTE", "EXIT", "PRICE/HR", "CREATED"}, Rows: rows}

				return output.Result{Table: table, Footer: footer}
			})

			return nil
		},
	}

	shared.Option(cmd, &opts.page, "--page <number>", "1", "page number")
	shared.Option(cmd, &opts.limit, "--limit <number>", "20", "Run Jobs per page (1-100)")
	shared.Option(cmd, &opts.profile, "--profile <name>", "", "profile to use")
	completion.Flag(cmd, "profile", shared.Profiles)

	return cmd
}
