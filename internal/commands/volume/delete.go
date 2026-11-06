package volume

import (
	"strings"

	"github.com/spf13/cobra"

	"featherless.ai/featherless-cloud/internal/api"
	"featherless.ai/featherless-cloud/internal/commands/shared"
	"featherless.ai/featherless-cloud/internal/completion"
	"featherless.ai/featherless-cloud/internal/output"
)

func newDeleteCommand() *cobra.Command {
	var opts struct {
		yes     bool
		profile string
	}

	cmd := &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a network volume",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			out := shared.NewContext(cmd)
			ctx := cmd.Context()
			client, err := shared.Client(opts.profile)

			if err != nil {
				return err
			}

			volume, err := output.Spin(out, "Finding volume", func() (api.NetworkVolume, error) {
				return findAndGetVolume(ctx, client, args[0])
			})

			if err != nil {
				return err
			}

			attached := volume.Attachments

			if len(attached) > 0 {
				lines := make([]string, len(attached))

				for index, attachment := range attached {
					lines[index] = "  " + attachment.Name + "  " + attachment.MountPath
				}

				out.Warn("Attached to:\n" + strings.Join(lines, "\n") + "\nDeleting detaches it from these instances, which must be stopped. They'll start without it, and files written to those paths won't be kept.")
			}

			if err := shared.RequireDeletion(out, "volume "+volume.Name, opts.yes); err != nil {
				return err
			}

			_, err = output.Spin(out, "Deleting volume", func() (int, error) {
				return client.DeleteVolume(ctx, volume.ID, len(attached) > 0)
			})

			if err != nil {
				return err
			}

			out.Success("Volume deletion started for " + volume.Name + ".")

			return nil
		},
	}

	completion.Args(cmd, completion.Arg{Name: "id", Complete: shared.Volumes})
	shared.Switch(cmd, &opts.yes, "--yes", "skip confirmation")
	shared.Option(cmd, &opts.profile, "--profile <name>", "", "profile to use")
	completion.Flag(cmd, "profile", shared.Profiles)

	return cmd
}
