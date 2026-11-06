package registrykeys

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
		Short: "Remove a registry key",
		Args:  shared.Arguments,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := shared.NewContext(cmd)

			client, err := shared.Client(opts.profile)
			if err != nil {
				return err
			}

			keys, err := output.Spin(ctx, "Finding registry key", func() ([]api.RegistryCredential, error) {
				return client.RegistryCredentials(cmd.Context())
			})

			if err != nil {
				return err
			}

			id, err := shared.ResolveCredentialRef(keys, args[0])
			if err != nil {
				return err
			}

			name := ""

			for _, key := range keys {
				if key.ID == id {
					name = key.Name
				}
			}

			if err := shared.RequireDeletion(ctx, "registry key "+name, opts.yes); err != nil {
				return err
			}

			_, err = output.Spin(ctx, "Removing registry key", func() (int, error) {
				return client.DeleteRegistryCredential(cmd.Context(), id)
			})

			if err != nil {
				return explainForbidden(err)
			}

			ctx.Success("Removed registry key " + name + ".")

			return nil
		},
	}

	completion.Args(cmd, completion.Arg{Name: "key", Complete: shared.Registries})
	shared.Switch(cmd, &opts.yes, "--yes", "skip confirmation")
	shared.Option(cmd, &opts.profile, "--profile <name>", "", "profile to use")
	completion.Flag(cmd, "profile", shared.Profiles)

	return cmd
}
