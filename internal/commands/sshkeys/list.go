package sshkeys

import (
	"github.com/spf13/cobra"

	"featherless.ai/featherless-cloud/internal/commands/shared"
	"featherless.ai/featherless-cloud/internal/completion"
	"featherless.ai/featherless-cloud/internal/output"
)

func newListCommand() *cobra.Command {
	var opts struct {
		profile string
	}

	cmd := &cobra.Command{
		Use:   "list",
		Short: "List your SSH public keys",
		Args:  shared.Arguments,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := shared.NewContext(cmd)

			client, err := shared.Client(opts.profile)
			if err != nil {
				return err
			}

			keys, err := client.SSHKeys(cmd.Context())

			if err != nil {
				return err
			}

			ctx.Output(keys, func() output.Result {
				if len(keys) == 0 {
					return output.Result{Text: "No SSH keys. Add one with featherless-cloud ssh-keys add."}
				}

				rows := make([][]string, len(keys))

				for i, key := range keys {
					rows[i] = []string{key.Name, key.Fingerprint, key.ID, shared.Slice(key.CreatedAt, 10)}
				}

				table := &output.Table{Header: []string{"NAME", "FINGERPRINT", "ID", "ADDED"}, Rows: rows}

				return output.Result{Table: table}
			})

			return nil
		},
	}

	shared.Option(cmd, &opts.profile, "--profile <name>", "", "profile to use")
	completion.Flag(cmd, "profile", shared.Profiles)

	return cmd
}
