package registrykeys

import (
	"io"
	"strings"

	"github.com/spf13/cobra"

	"featherless.ai/featherless-cloud/internal/api"
	"featherless.ai/featherless-cloud/internal/commands/shared"
	"featherless.ai/featherless-cloud/internal/completion"
	"featherless.ai/featherless-cloud/internal/errs"
	"featherless.ai/featherless-cloud/internal/output"
)

// readSecret reads the registry password from stdin, or prompts for it; never
// from a flag, so it stays out of shell history.
func readSecret(ctx *output.Context, fromStdin bool) (string, error) {
	if fromStdin {
		data, err := io.ReadAll(ctx.Stdin)
		if err != nil {
			return "", err
		}

		secret := string(data)

		if strings.HasSuffix(secret, "\n") {
			secret = strings.TrimSuffix(strings.TrimSuffix(secret, "\n"), "\r")
		}

		if secret == "" {
			return "", errs.New("No password on stdin.")
		}

		return secret, nil
	}

	if ctx.Mode != output.Interactive {
		return "", errs.New("Pass the password or access token on stdin with --password-stdin.")
	}

	validate := func(value string) error {
		if value == "" {
			return errs.New("Enter the password or access token.")
		}

		return nil
	}

	secret, err := ctx.Prompt.Password("Password or access token", validate)
	if err != nil {
		return "", shared.PromptFailure(ctx, err)
	}

	return secret, nil
}

// explainForbidden says why a change was refused: registry keys belong to the
// organization, and only owners and admins may change them.
func explainForbidden(err error) error {
	if errs.IsKind(err, errs.KindForbidden) {
		return errs.New("Only organization owners and admins can change registry keys, with an API key that has Account write access.")
	}

	return err
}

func idText(key api.RegistryCredential) func() output.Result {
	return func() output.Result { return output.Result{Text: key.ID} }
}

func newAddCommand() *cobra.Command {
	var opts struct {
		server        string
		username      string
		passwordStdin bool
		profile       string
	}

	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Add a registry login for pulling private images",
		Args:  shared.Arguments,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := shared.NewContext(cmd)

			auth, err := shared.ResolveAuth(opts.profile)
			if err != nil {
				return err
			}

			secret, err := readSecret(ctx, opts.passwordStdin)
			if err != nil {
				return err
			}

			client := shared.NewCloud(auth.APIBase, auth.APIKey)
			body := api.RegistryCredentialBody{Name: args[0], Registry: opts.server, Username: opts.username, Secret: secret}

			key, err := output.Spin(ctx, "Adding registry key", func() (api.RegistryCredential, error) {
				return client.AddRegistryCredential(cmd.Context(), body)
			})

			if err != nil {
				return explainForbidden(err)
			}

			ctx.Success("Added registry key " + key.Name + " for " + key.Registry + ". Use it with --registry " + key.Name + ".")
			ctx.Output(key, idText(key))

			return nil
		},
	}

	completion.Args(cmd, completion.Arg{Name: "name", Description: "name to pass with --registry"})
	shared.Option(cmd, &opts.server, "--server <host>", "", "registry host, e.g. docker.io or ghcr.io")
	shared.Option(cmd, &opts.username, "--username <user>", "", "registry username")
	shared.Switch(cmd, &opts.passwordStdin, "--password-stdin", "read the password or access token from stdin")
	shared.Option(cmd, &opts.profile, "--profile <name>", "", "profile to use")
	completion.Flag(cmd, "profile", shared.Profiles)
	_ = cmd.MarkFlagRequired("server")
	_ = cmd.MarkFlagRequired("username")
	shared.HelpAfter(cmd, "\nExamples:\n  featherless-cloud registry-keys add ghcr --server ghcr.io --username octocat\n  echo \"$GHCR_TOKEN\" | featherless-cloud registry-keys add ghcr --server ghcr.io --username octocat --password-stdin\n\nUse it with: featherless-cloud exec --image ghcr.io/octocat/app:1 --registry ghcr -- …")

	return cmd
}
