package instance

import (
	"github.com/spf13/cobra"

	"featherless.ai/featherless-cloud/internal/commands/shared"
	"featherless.ai/featherless-cloud/internal/completion"
	"featherless.ai/featherless-cloud/internal/errs"
)

func newListCommand() *cobra.Command {
	var opts struct {
		page    string
		limit   string
		profile string
	}

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List GPU instances",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return errs.New("not implemented")
		},
	}

	shared.Option(cmd, &opts.page, "--page <number>", "1", "page number")
	shared.Option(cmd, &opts.limit, "--limit <number>", "20", "instances per page (1-100)")
	shared.Option(cmd, &opts.profile, "--profile <name>", "", "profile to use")
	completion.Flag(cmd, "profile", shared.Profiles)

	return cmd
}
