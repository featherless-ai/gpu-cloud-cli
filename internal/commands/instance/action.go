package instance

import (
	"github.com/spf13/cobra"

	"featherless.ai/featherless-cloud/internal/api"
	"featherless.ai/featherless-cloud/internal/commands/shared"
	"featherless.ai/featherless-cloud/internal/completion"
	"featherless.ai/featherless-cloud/internal/errs"
)

var actionDescriptions = map[api.InstanceAction]string{
	api.ActionStart:   "Start",
	api.ActionStop:    "Stop",
	api.ActionRestart: "Restart",
	api.ActionDelete:  "Delete",
}

func newActionCommand(action api.InstanceAction) *cobra.Command {
	var opts struct {
		profile string
		yes     bool
	}

	cmd := &cobra.Command{
		Use:   string(action) + " <id>",
		Short: actionDescriptions[action] + " an instance",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return errs.New("not implemented")
		},
	}

	completion.Args(cmd, completion.Arg{Name: "id", Complete: shared.Instances})
	shared.Option(cmd, &opts.profile, "--profile <name>", "", "profile to use")
	completion.Flag(cmd, "profile", shared.Profiles)

	if action == api.ActionDelete {
		shared.Switch(cmd, &opts.yes, "--yes", "skip confirmation")
	}

	return cmd
}
