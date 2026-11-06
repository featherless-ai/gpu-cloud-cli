package sshkeys

import (
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
		Use:   "delete <key>",
		Short: "Remove an SSH public key",
		Args:  shared.Arguments,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := shared.NewContext(cmd)

			client, err := shared.Client(opts.profile)
			if err != nil {
				return err
			}

			keys, err := output.Spin(ctx, "Finding SSH key", func() ([]api.SSHKey, error) {
				return client.SSHKeys(cmd.Context())
			})

			if err != nil {
				return err
			}

			key, err := shared.ResolveSSHKeyRef(keys, args[0])
			if err != nil {
				return err
			}

			if err := shared.RequireDeletion(ctx, "SSH key "+key.Name, opts.yes); err != nil {
				return err
			}

			_, err = output.Spin(ctx, "Removing SSH key", func() (int, error) {
				return client.DeleteSSHKey(cmd.Context(), key.ID)
			})

			if err != nil {
				return err
			}

			ctx.Success("Removed SSH key " + key.Name + ".")

			return nil
		},
	}

	completion.Args(cmd, completion.Arg{Name: "key", Complete: shared.SSHKeys})
	shared.Switch(cmd, &opts.yes, "--yes", "skip confirmation")
	shared.Option(cmd, &opts.profile, "--profile <name>", "", "profile to use")
	completion.Flag(cmd, "profile", shared.Profiles)

	return cmd
}
