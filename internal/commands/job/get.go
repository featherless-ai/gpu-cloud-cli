package job

import (
	"fmt"
	"math/big"
	"strings"

	"github.com/spf13/cobra"

	"featherless.ai/featherless-cloud/internal/api"
	"featherless.ai/featherless-cloud/internal/commands/shared"
	"featherless.ai/featherless-cloud/internal/completion"
	"featherless.ai/featherless-cloud/internal/output"
	"featherless.ai/featherless-cloud/internal/version"
)

// waitingLabels say why a queued Run Job has not been admitted; anything
// else is just waiting.
var waitingLabels = map[string]string{
	"queue_credit":   "waiting for credit",
	"queue_quota":    "waiting for organization limit",
	"queue_capacity": "waiting for cluster capacity",
}

// listHourly is what the job would cost an hour without its discount.
func listHourly(job api.RunJob, discount api.ComputeDiscount) string {
	quantity := int64(job.Resources.CPUCores)

	if job.ComputeType == api.ComputeGPU {
		quantity = int64(job.GPUCount)
	}

	listPrice, _ := new(big.Int).SetString(discount.ListUnitPriceMicros, 10)
	unitPrice, _ := new(big.Int).SetString(discount.UnitPriceMicros, 10)
	cost, _ := new(big.Int).SetString(job.CostMicrosPerHour, 10)

	if listPrice == nil || unitPrice == nil || cost == nil {
		return job.CostMicrosPerHour
	}

	saved := new(big.Int).Mul(new(big.Int).Sub(listPrice, unitPrice), big.NewInt(quantity))

	return new(big.Int).Add(cost, saved).String()
}

func describeJob(item api.RunJob) string {
	state := string(item.State)

	if isWaiting(item) {
		reason := ""

		if item.StatusReason != nil {
			reason = *item.StatusReason
		}

		state = "waiting"

		if label, isKnown := waitingLabels[reason]; isKnown {
			state = label
		}
	}

	compute := fmt.Sprintf("%d vCPU / %d GiB", item.Resources.CPUCores, item.Resources.MemoryGiB)

	if item.ComputeType == api.ComputeGPU {
		compute = fmt.Sprintf("%d x %s", item.GPUCount, gpuModelOr(item, item.ComputeSKU))
	}

	exitCode := "-"

	if item.ExitCode != nil {
		exitCode = fmt.Sprint(*item.ExitCode)
	}

	lines := []string{
		"NAME        " + item.Name,
		"SHORT ID    " + item.ShortID,
		"STATE       " + state,
		"COMPUTE     " + compute,
		"HOURLY      " + version.FormatDollars(item.CostMicrosPerHour) + "/hr",
	}

	if discount := item.ComputeDiscount.Value; discount != nil {
		lines = append(lines, "DISCOUNT    "+discount.Name+", list price "+version.FormatDollars(listHourly(item, *discount))+"/hr")
	}

	lines = append(lines,
		"IMAGE       "+item.Image.Reference,
		"COMMAND     "+strings.Join(item.Command, " "),
		fmt.Sprintf("TIMEOUT     %ds after compute assignment", item.TimeoutSeconds),
		"EXIT CODE   "+exitCode,
		fmt.Sprintf("RETRIES     %d if interrupted for other work", item.PreemptionRetries),
	)

	if len(item.Attempts) > 1 {
		for _, attempt := range item.Attempts {
			started := ""

			if attempt.StartedAt != nil && *attempt.StartedAt != "" {
				started = ", started " + *attempt.StartedAt
			}

			lines = append(lines, fmt.Sprintf("ATTEMPT %d   %s%s", attempt.Number, attempt.State, started))
		}
	}

	for _, mount := range item.NetworkVolumeMounts {
		lines = append(lines, "VOLUME      "+mount.Name+" → "+mount.MountPath)
	}

	lines = append(lines, "CREATED     "+item.CreatedAt)

	return strings.Join(lines, "\n")
}

func newGetCommand() *cobra.Command {
	var opts struct {
		profile string
	}

	cmd := &cobra.Command{
		Use:   "get <id>",
		Short: "Show one Run Job",
		Args:  shared.Arguments,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := shared.NewContext(cmd)

			client, err := shared.Client(opts.profile)
			if err != nil {
				return err
			}

			job, err := client.GetJob(cmd.Context(), args[0])

			if err != nil {
				return err
			}

			if job.StatusMessage != nil && *job.StatusMessage != "" {
				ctx.Warn(*job.StatusMessage)
			}

			ctx.Output(job, func() output.Result { return output.Result{Text: describeJob(job)} })

			return nil
		},
	}

	completion.Args(cmd, completion.Arg{Name: "id", Description: "Run Job short id", Complete: shared.Jobs})
	shared.Option(cmd, &opts.profile, "--profile <name>", "", "profile to use")
	completion.Flag(cmd, "profile", shared.Profiles)

	return cmd
}
