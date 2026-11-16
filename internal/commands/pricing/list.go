package pricing

import (
	"strconv"

	"github.com/spf13/cobra"

	"featherless.ai/featherless-cloud/internal/api"
	"featherless.ai/featherless-cloud/internal/commands/shared"
	"featherless.ai/featherless-cloud/internal/completion"
	"featherless.ai/featherless-cloud/internal/output"
	"featherless.ai/featherless-cloud/internal/version"
)

var unitLabels = map[api.PriceUnit]string{
	api.UnitGPUHour:  "/GPU-hr",
	api.UnitVCPUHour: "/vCPU-hr",
	api.UnitGiBMonth: "/GiB-mo",
	api.UnitGiB:      "/GiB",
}

func rateRow(rate api.PriceRate) []string {
	gpus := "-"

	if rate.GPUCount != nil {
		gpus = strconv.Itoa(*rate.GPUCount)
	}

	cpus, memory, gpuMemory := "-", "-", "-"

	if resources := rate.Resources; resources != nil {
		cpus = strconv.Itoa(resources.CPUCores)
		memory = strconv.Itoa(resources.MemoryGiB)

		if resources.GPUMemoryGiB != nil && *resources.GPUMemoryGiB != 0 {
			gpuMemory = strconv.Itoa(*resources.GPUMemoryGiB)
		}
	}

	compareAt := "-"

	if rate.CompareAtUnitPriceMicros != nil && *rate.CompareAtUnitPriceMicros != "" {
		compareAt = version.FormatDollars(*rate.CompareAtUnitPriceMicros)
	}

	price := version.FormatDollars(rate.UnitPriceMicros) + unitLabels[rate.Unit]

	return []string{rate.DisplayName, gpus, cpus, memory, gpuMemory, price, compareAt}
}

func newListCommand() *cobra.Command {
	var opts struct {
		workload string
		all      bool
		profile  string
	}

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List current prices",
		Args:  shared.Arguments,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := shared.NewContext(cmd)

			client, err := shared.Client(opts.profile)
			if err != nil {
				return err
			}

			rates, err := client.Pricing(cmd.Context(), api.Workload(opts.workload))

			if err != nil {
				return err
			}

			ctx.Output(rates, func() output.Result {
				var rows [][]string

				for _, rate := range rates {
					if opts.all || api.ComputeTypeOf(rate) != "" {
						rows = append(rows, rateRow(rate))
					}
				}

				header := []string{"OFFER", "GPUS", "vCPU", "RAM(GiB)", "GPU-MEM(GiB)", "PRICE", "COMPARE AT"}
				footer := ""

				if !opts.all {
					footer = "Add --all for storage and bandwidth rates."
				}

				return output.Result{Table: &output.Table{Header: header, Rows: rows}, Footer: footer}
			})

			return nil
		},
	}

	shared.Option(cmd, &opts.workload, "--workload <kind>", string(api.WorkloadInstance), "prices for instance or run-job")
	completion.Choices(cmd, "workload", string(api.WorkloadInstance), string(api.WorkloadRunJob))
	shared.Switch(cmd, &opts.all, "--all", "include storage and bandwidth rates")
	shared.Option(cmd, &opts.profile, "--profile <name>", "", "profile to use")
	completion.Flag(cmd, "profile", shared.Profiles)

	return cmd
}
