package billing

import (
	"math/big"
	"strings"

	"github.com/spf13/cobra"

	"featherless.ai/featherless-cloud/internal/api"
	"featherless.ai/featherless-cloud/internal/commands/shared"
	"featherless.ai/featherless-cloud/internal/completion"
	"featherless.ai/featherless-cloud/internal/output"
	"featherless.ai/featherless-cloud/internal/version"
)

const secondsPerHour = 3600

func entryDate(entry api.LedgerEntry) string {
	start := shared.Slice(entry.ChargedAt, 10)
	end := shared.Slice(entry.ChargedUntil, 10)

	if start == end {
		return start
	}

	return start + "–" + end
}

// toFixed2 is JavaScript's number.toFixed(2): exact halves round away from zero.
func toFixed2(value float64) string {
	exact := new(big.Rat)

	if exact.SetFloat64(value) == nil {
		return shared.FormatNumber(value)
	}

	return exact.FloatString(2)
}

// entryDescription words a metered line from its quantity and unit, as the
// console does, rather than sending a sentence.
func entryDescription(entry api.LedgerEntry) string {
	if entry.ResourceKind == nil || *entry.ResourceKind == "" {
		return entry.Description
	}

	hours := ""

	if entry.Quantity != nil && *entry.Quantity != "" {
		hours = toFixed2(shared.ParseNumber(*entry.Quantity) / secondsPerHour)
	}

	measure := ""

	if entry.SKUDisplayName != nil {
		measure = *entry.SKUDisplayName
	}

	unit := ""

	if entry.Unit != nil {
		unit = *entry.Unit
	}

	if hours != "" && unit == "gpu_second" {
		measure = hours + " GPU-hours"
	} else if hours != "" && unit == "vcpu_second" {
		measure = hours + " vCPU-hours"
	}

	var parts []string

	if measure != "" {
		parts = append(parts, measure)
	}

	if entry.ResourceName != nil && *entry.ResourceName != "" {
		parts = append(parts, *entry.ResourceName)
	}

	return strings.Join(parts, " · ")
}

func amount(micros string) string {
	dollars := version.FormatDollars(micros)

	if shared.ParseNumber(micros) > 0 {
		return "+" + dollars
	}

	return dollars
}

func newHistoryCommand() *cobra.Command {
	var opts struct {
		profile string
	}

	cmd := &cobra.Command{
		Use:   "history",
		Short: "List charges and top-ups",
		Args:  shared.Arguments,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := shared.NewContext(cmd)

			client, err := shared.Client(opts.profile)
			if err != nil {
				return err
			}

			ledger, err := client.BillingLedger(cmd.Context())

			if err != nil {
				return err
			}

			ctx.Output(ledger, func() output.Result {
				rows := make([][]string, len(ledger.Items))

				for i, entry := range ledger.Items {
					rows[i] = []string{entryDate(entry), string(entry.Type), entryDescription(entry), amount(entry.AmountMicros)}
				}

				table := &output.Table{Header: []string{"DATE", "TYPE", "DESCRIPTION", "AMOUNT"}, Rows: rows, RightAligned: []int{3}}
				footer := shared.FormatNumber(float64(len(ledger.Items))) + " of " + shared.FormatNumber(ledger.Pagination.TotalItems) + " entry(s)"

				return output.Result{Table: table, Footer: footer}
			})

			return nil
		},
	}

	shared.Option(cmd, &opts.profile, "--profile <name>", "", "profile to use")
	completion.Flag(cmd, "profile", shared.Profiles)

	return cmd
}
