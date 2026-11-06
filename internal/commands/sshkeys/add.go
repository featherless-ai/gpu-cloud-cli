package sshkeys

import (
	"github.com/spf13/cobra"

	"featherless.ai/featherless-cloud/internal/commands/shared"
	"featherless.ai/featherless-cloud/internal/completion"
	"featherless.ai/featherless-cloud/internal/errs"
)

// parsePublicKey checks an OpenSSH public key line and returns it with its comment.
func parsePublicKey(content string) (publicKey, comment string, err error) {
	return "", "", errs.New("not implemented")
}

func newAddCommand() *cobra.Command {
	var opts struct {
		name    string
		profile string
	}

	cmd := &cobra.Command{
		Use:   "add [file]",
		Short: "Add an SSH public key for instance access",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return errs.New("not implemented")
		},
	}

	completion.Args(cmd, completion.Arg{Name: "file"})
	shared.Option(cmd, &opts.name, "--name <name>", "", "key name (default: the key comment, or this computer's name)")
	shared.Option(cmd, &opts.profile, "--profile <name>", "", "profile to use")
	completion.Flag(cmd, "profile", shared.Profiles)

	return cmd
}
