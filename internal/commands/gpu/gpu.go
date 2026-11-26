// Package gpu is the `gpu` command group: the GPU models a project can run on.
package gpu

import (
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"featherless.ai/featherless-cloud/internal/api"
	"featherless.ai/featherless-cloud/internal/commands/shared"
	"featherless.ai/featherless-cloud/internal/completion"
	"featherless.ai/featherless-cloud/internal/output"
	"featherless.ai/featherless-cloud/internal/version"
)

// Command is the `gpu` group.
func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gpu",
		Short: "List the GPU models you can run on",
	}

	cmd.AddCommand(newListCommand())

	return cmd
}

// model is one GPU model. Model is the value --gpu-type and the project file's
// gpu_type take; the rest is what choosing it buys.
type model struct {
	Model               string `json:"model"`
	MemoryGiB           *int   `json:"memoryGiB,omitempty"`
	GPUCounts           []int  `json:"gpuCounts"`
	InstanceSKU         string `json:"instanceSku,omitempty"`
	InstancePriceMicros string `json:"instancePriceMicros,omitempty"`
	RunJobSKU           string `json:"runJobSku,omitempty"`
	RunJobPriceMicros   string `json:"runJobPriceMicros,omitempty"`
}

func newListCommand() *cobra.Command {
	var opts struct {
		profile string
	}

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List each GPU model, its memory, GPU counts and hourly price",
		Args:  shared.Arguments,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := shared.NewContext(cmd)

			client, err := shared.Client(opts.profile)
			if err != nil {
				return err
			}

			instance, instanceErr := client.Pricing(cmd.Context(), api.WorkloadInstance)
			runJob, runJobErr := client.Pricing(cmd.Context(), api.WorkloadRunJob)

			if err := shared.FirstError(instanceErr, runJobErr); err != nil {
				return err
			}

			models := collect(instance, runJob)

			ctx.Output(models, func() output.Result {
				rows := make([][]string, len(models))

				for i, entry := range models {
					rows[i] = []string{
						entry.Model,
						memory(entry.MemoryGiB),
						counts(entry.GPUCounts),
						price(entry.InstancePriceMicros),
						price(entry.RunJobPriceMicros),
					}
				}

				return output.Result{
					Table: &output.Table{
						Header: []string{"MODEL", "MEMORY(GiB)", "GPUS", "INSTANCE", "RUN JOB"},
						Rows:   rows,
					},
					Footer: "Use the model with --gpu-type, or as gpu_type in .featherless-cloud.toml.",
				}
			})

			return nil
		},
	}

	shared.Option(cmd, &opts.profile, "--profile <name>", "", "profile to use")
	completion.Flag(cmd, "profile", shared.Profiles)

	return cmd
}

// collect merges both workloads' prices into one entry per model, sorted by
// model name. The price kept is the single-GPU rate, which scales to every
// count; the counts still available are listed.
func collect(instance, runJob []api.PriceRate) []model {
	byName := map[string]*model{}

	merge := func(rates []api.PriceRate, isInstance bool) {
		for _, rate := range rates {
			name := modelName(rate)

			if name == "" {
				continue
			}

			entry, seen := byName[name]

			if !seen {
				entry = &model{Model: name, GPUCounts: []int{}}
				byName[name] = entry
			}

			if entry.MemoryGiB == nil && rate.Resources.GPUMemoryGiB != nil && *rate.Resources.GPUMemoryGiB != 0 {
				entry.MemoryGiB = rate.Resources.GPUMemoryGiB
			}

			if rate.GPUCount != nil && *rate.GPUCount > 0 && !slices.Contains(entry.GPUCounts, *rate.GPUCount) {
				entry.GPUCounts = append(entry.GPUCounts, *rate.GPUCount)
				slices.Sort(entry.GPUCounts)
			}

			if rate.GPUCount == nil || *rate.GPUCount != 1 {
				continue
			}

			if isInstance {
				entry.InstanceSKU = rate.SKU
				entry.InstancePriceMicros = rate.UnitPriceMicros

				continue
			}

			entry.RunJobSKU = rate.SKU
			entry.RunJobPriceMicros = rate.UnitPriceMicros
		}
	}

	merge(instance, true)
	merge(runJob, false)

	models := make([]model, 0, len(byName))

	for _, entry := range byName {
		models = append(models, *entry)
	}

	slices.SortFunc(models, func(a, b model) int { return strings.Compare(a.Model, b.Model) })

	return models
}

// modelName is the GPU model a priced offer sells, or "" when the rate is not
// a GPU offer with a model name.
func modelName(rate api.PriceRate) string {
	if api.ComputeTypeOf(rate) != api.ComputeGPU || rate.Unit != api.UnitGPUHour || rate.Resources == nil {
		return ""
	}

	if rate.Resources.GPUModel == nil {
		return ""
	}

	return *rate.Resources.GPUModel
}

func memory(value *int) string {
	if value == nil {
		return "-"
	}

	return strconv.Itoa(*value)
}

func counts(values []int) string {
	if len(values) == 0 {
		return "-"
	}

	parts := make([]string, len(values))

	for i, value := range values {
		parts[i] = strconv.Itoa(value)
	}

	return strings.Join(parts, ", ")
}

func price(micros string) string {
	if micros == "" {
		return "-"
	}

	return version.FormatDollars(micros) + "/GPU-hr"
}
