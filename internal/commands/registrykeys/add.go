package registrykeys

import (
	"github.com/spf13/cobra"

	"featherless.ai/featherless-cloud/internal/commands/shared"
	"featherless.ai/featherless-cloud/internal/completion"
	"featherless.ai/featherless-cloud/internal/errs"
	"featherless.ai/featherless-cloud/internal/output"
)

// readSecret reads the registry password from stdin, or prompts for it; never
// from a flag, so it stays out of shell history.
func readSecret(ctx *output.Context, fromStdin bool) (string, error) {
	return "", errs.New("not implemented")
}

// explainForbidden says why a change was refused: registry keys belong to the
// organization, and only owners and admins may change them.
func explainForbidden(err error) error {
	return err
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
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return errs.New("not implemented")
		},
	}

	completion.Args(cmd, completion.Arg{Name: "name"})
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
