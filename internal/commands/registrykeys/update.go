package registrykeys

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
		name           string
		server         string
		username       string
		changePassword bool
		passwordStdin  bool
		profile        string
	}

	cmd := &cobra.Command{
		Use:   "update <key>",
		Short: "Change a registry key",
		Args:  shared.Arguments,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := shared.NewContext(cmd)
			var body api.UpdateRegistryCredentialBody
			hasChanges := false

			if opts.name != "" {
				body.Name = &opts.name
				hasChanges = true
			}

			if opts.server != "" {
				body.Registry = &opts.server
				hasChanges = true
			}

			if opts.username != "" {
				body.Username = &opts.username
				hasChanges = true
			}

			if opts.changePassword || opts.passwordStdin {
				secret, err := readSecret(ctx, opts.passwordStdin)
				if err != nil {
					return err
				}

				body.Secret = &secret
				hasChanges = true
			}

			if !hasChanges {
				return errs.New("Nothing to change. Pass --name, --server, --username, --change-password or --password-stdin.")
			}

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

			key, err := output.Spin(ctx, "Updating registry key", func() (api.RegistryCredential, error) {
				return client.UpdateRegistryCredential(cmd.Context(), id, body)
			})

			if err != nil {
				return explainForbidden(err)
			}

			ctx.Success("Updated registry key " + key.Name + ".")
			ctx.Output(key, idText(key))

			return nil
		},
	}

	completion.Args(cmd, completion.Arg{Name: "key", Complete: shared.Registries})
	shared.Option(cmd, &opts.name, "--name <name>", "", "new name")
	shared.Option(cmd, &opts.server, "--server <host>", "", "new registry host")
	shared.Option(cmd, &opts.username, "--username <user>", "", "new registry username")
	shared.Switch(cmd, &opts.changePassword, "--change-password", "prompt for a new password or access token")
	shared.Switch(cmd, &opts.passwordStdin, "--password-stdin", "read a new password or access token from stdin")
	shared.Option(cmd, &opts.profile, "--profile <name>", "", "profile to use")
	completion.Flag(cmd, "profile", shared.Profiles)

	return cmd
}
