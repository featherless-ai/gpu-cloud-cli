package instance

import (
	"github.com/spf13/cobra"

	"featherless.ai/featherless-cloud/internal/api"
	"featherless.ai/featherless-cloud/internal/commands/shared"
	"featherless.ai/featherless-cloud/internal/completion"
	"featherless.ai/featherless-cloud/internal/output"
)

var (
	actionPastTense = map[api.InstanceAction]string{
		api.ActionStart:   "started",
		api.ActionStop:    "stopped",
		api.ActionRestart: "restarted",
		api.ActionDelete:  "deleted",
	}
	actionProgress = map[api.InstanceAction]string{
		api.ActionStart:   "Starting",
		api.ActionStop:    "Stopping",
		api.ActionRestart: "Restarting",
		api.ActionDelete:  "Deleting",
	}
	actionDescriptions = map[api.InstanceAction]string{
		api.ActionStart:   "Start",
		api.ActionStop:    "Stop",
		api.ActionRestart: "Restart",
		api.ActionDelete:  "Delete",
	}
)

func newActionCommand(action api.InstanceAction) *cobra.Command {
	var opts struct {
		profile string
		yes     bool
	}

	cmd := &cobra.Command{
		Use:   string(action) + " <id>",
		Short: actionDescriptions[action] + " an instance",
		Args:  shared.Arguments,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := shared.NewContext(cmd)
			rawID := args[0]

			client, err := shared.Client(opts.profile)
			if err != nil {
				return err
			}

			id, err := shared.ResolveInstanceID(cmd.Context(), client, rawID)
			if err != nil {
				return err
			}

			if action == api.ActionDelete {
				if err := shared.RequireDeletion(ctx, "instance "+rawID, opts.yes); err != nil {
					return err
				}
			}

			instance, err := output.Spin(ctx, actionProgress[action]+" instance", func() (api.Instance, error) {
				return client.InstanceAction(cmd.Context(), id, action)
			})

			if err != nil {
				return err
			}

			if action == api.ActionDelete {
				ctx.Success("Instance deletion started for " + instance.Name + " (" + instance.ShortID + ").")
			} else {
				ctx.Success("Instance " + instance.Name + " " + actionPastTense[action] + ".")
			}

			ctx.Output(instance, func() output.Result { return output.Result{Text: "STATE " + string(instance.State)} })

			return nil
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
