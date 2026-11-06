package pricing

import (
	"github.com/spf13/cobra"

	"featherless.ai/featherless-cloud/internal/api"
	"featherless.ai/featherless-cloud/internal/commands/shared"
	"featherless.ai/featherless-cloud/internal/completion"
	"featherless.ai/featherless-cloud/internal/errs"
)

func newListCommand() *cobra.Command {
	var opts struct {
		workload string
		all      bool
		profile  string
	}

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List current prices",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return errs.New("not implemented")
		},
	}

	shared.Option(cmd, &opts.workload, "--workload <kind>", string(api.WorkloadInstance), "prices for instance or run-job")
	completion.Choices(cmd, "workload", string(api.WorkloadInstance), string(api.WorkloadRunJob))
	shared.Switch(cmd, &opts.all, "--all", "include storage and bandwidth rates")
	shared.Option(cmd, &opts.profile, "--profile <name>", "", "profile to use")
	completion.Flag(cmd, "profile", shared.Profiles)

	return cmd
}
