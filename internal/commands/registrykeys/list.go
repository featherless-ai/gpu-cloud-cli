package registrykeys

import (
	"github.com/spf13/cobra"

	"featherless.ai/featherless-cloud/internal/api"
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
		Short: "List registry keys",
		Args:  shared.Arguments,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := shared.NewContext(cmd)

			client, err := shared.Client(opts.profile)
			if err != nil {
				return err
			}

			keys, err := output.Spin(ctx, "Fetching registry keys", func() ([]api.RegistryCredential, error) {
				return client.RegistryCredentials(cmd.Context())
			})

			if err != nil {
				return err
			}

			ctx.Output(keys, func() output.Result {
				if len(keys) == 0 {
					return output.Result{Text: "No registry keys. Add one with featherless-cloud registry-keys add."}
				}

				rows := make([][]string, len(keys))

				for i, key := range keys {
					rows[i] = []string{key.Name, key.Registry, key.Username, key.ID, shared.Slice(key.UpdatedAt, 10)}
				}

				table := &output.Table{Header: []string{"NAME", "SERVER", "USERNAME", "ID", "UPDATED"}, Rows: rows}

				return output.Result{Table: table}
			})

			return nil
		},
	}

	shared.Option(cmd, &opts.profile, "--profile <name>", "", "profile to use")
	completion.Flag(cmd, "profile", shared.Profiles)

	return cmd
}
