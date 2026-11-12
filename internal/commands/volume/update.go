package volume

import (
	"github.com/spf13/cobra"

	"featherless.ai/featherless-cloud/internal/api"
	"featherless.ai/featherless-cloud/internal/commands/shared"
	"featherless.ai/featherless-cloud/internal/completion"
	"featherless.ai/featherless-cloud/internal/errs"
	"featherless.ai/featherless-cloud/internal/output"
)

func newUpdateCommand() *cobra.Command {
	var opts struct {
		name    string
		gb      string
		profile string
	}

	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Rename or grow a network volume",
		Args:  shared.Arguments,
		RunE: func(cmd *cobra.Command, args []string) error {
			out := shared.NewContext(cmd)
			ctx := cmd.Context()
			client, err := shared.Client(opts.profile)

			if err != nil {
				return err
			}

			isRenamed := cmd.Flags().Changed("name")
			isGrown := cmd.Flags().Changed("gb")

			if !isRenamed && !isGrown {
				return out.Fatal("Nothing to update.")
			}

			volume, err := output.Spin(out, "Updating volume", func() (api.NetworkVolume, error) {
				current, err := findAndGetVolume(ctx, client, args[0])

				if err != nil {
					return api.NetworkVolume{}, err
				}

				var body api.UpdateVolumeBody

				if isRenamed {
					body.Name = &opts.name
				}

				if isGrown {
					capacity, err := parseCapacity(opts.gb)

					if err != nil {
						return api.NetworkVolume{}, err
					}

					if capacity < current.CapacityGiB {
						return api.NetworkVolume{}, errs.Newf("Volumes can only grow. Current capacity is %d GB.", current.CapacityGiB)
					}

					body.CapacityGiB = &capacity
				}

				return client.PatchVolume(ctx, current.ID, body)
			})

			if err != nil {
				return err
			}

			out.Success("Volume " + volume.Name + " updated.")
			out.Output(volume, func() output.Result {
				return output.Result{Text: "STATE " + string(volume.State) + " · " + itoa(volume.CapacityGiB) + " GiB"}
			})

			return nil
		},
	}

	completion.Args(cmd, completion.Arg{Name: "id", Description: "volume name or id", Complete: shared.Volumes})
	shared.Option(cmd, &opts.name, "--name <new>", "", "new volume name")
	shared.Option(cmd, &opts.gb, "--gb <n>", "", "new capacity in GB (grow-only, 10-10240)")
	shared.Option(cmd, &opts.profile, "--profile <name>", "", "profile to use")
	completion.Flag(cmd, "profile", shared.Profiles)

	return cmd
}
