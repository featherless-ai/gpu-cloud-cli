package job

import (
	"github.com/spf13/cobra"

	"featherless.ai/featherless-cloud/internal/api"
	"featherless.ai/featherless-cloud/internal/commands/shared"
	"featherless.ai/featherless-cloud/internal/completion"
	"featherless.ai/featherless-cloud/internal/output"
)

func stateText(job api.RunJob) func() output.Result {
	return func() output.Result { return output.Result{Text: "STATE " + string(job.State)} }
}

func newCancelCommand() *cobra.Command {
	var opts struct {
		profile string
	}

	cmd := &cobra.Command{
		Use:   "cancel <id>",
		Short: "Cancel a queued or running Run Job",
		Args:  shared.Arguments,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := shared.NewContext(cmd)

			client, err := shared.Client(opts.profile)
			if err != nil {
				return err
			}

			job, err := output.Spin(ctx, "Cancelling Run Job", func() (api.RunJob, error) {
				return client.CancelJob(cmd.Context(), args[0])
			})

			if err != nil {
				return err
			}

			ctx.Success("Cancellation started for " + job.Name + " (" + job.ShortID + ").")
			ctx.Output(job, stateText(job))

			return nil
		},
	}

	completion.Args(cmd, completion.Arg{Name: "id", Complete: shared.Jobs})
	shared.Option(cmd, &opts.profile, "--profile <name>", "", "profile to use")
	completion.Flag(cmd, "profile", shared.Profiles)

	return cmd
}

func newDeleteCommand() *cobra.Command {
	var opts struct {
		yes     bool
		profile string
	}

	cmd := &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a Run Job record",
		Args:  shared.Arguments,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := shared.NewContext(cmd)

			client, err := shared.Client(opts.profile)
			if err != nil {
				return err
			}

			if err := shared.RequireDeletion(ctx, "Run Job "+args[0], opts.yes); err != nil {
				return err
			}

			job, err := output.Spin(ctx, "Deleting Run Job", func() (api.RunJob, error) {
				return client.DeleteJob(cmd.Context(), args[0])
			})

			if err != nil {
				return err
			}

			ctx.Success("Run Job deletion started for " + job.Name + " (" + job.ShortID + ").")
			ctx.Output(job, stateText(job))

			return nil
		},
	}

	completion.Args(cmd, completion.Arg{Name: "id", Complete: shared.Jobs})
	shared.Switch(cmd, &opts.yes, "--yes", "skip confirmation")
	shared.Option(cmd, &opts.profile, "--profile <name>", "", "profile to use")
	completion.Flag(cmd, "profile", shared.Profiles)

	return cmd
}
