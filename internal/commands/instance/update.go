package instance

import (
	"github.com/spf13/cobra"

	"featherless.ai/featherless-cloud/internal/commands/shared"
	"featherless.ai/featherless-cloud/internal/completion"
	"featherless.ai/featherless-cloud/internal/errs"
)

type updateOptions struct {
	name     string
	image    string
	registry string
	disk     string
	command  string
	env      []string
	port     []string
	profile  string
}

// registriesOrNull completes --registry, where null switches to public pulls.
func registriesOrNull(word string, ctx *completion.Context) ([]string, error) {
	return nil, errs.New("not implemented")
}

func newUpdateCommand() *cobra.Command {
	var opts updateOptions

	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update an instance (config fields require a stopped instance)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return errs.New("not implemented")
		},
	}

	completion.Args(cmd, completion.Arg{Name: "id", Complete: shared.Instances})
	shared.Option(cmd, &opts.name, "--name <name>", "", "rename the instance")
	shared.Option(cmd, &opts.image, "--image <reference>", "", "container image (requires stopped state)")
	shared.Option(cmd, &opts.registry, "--registry <name|id|null>", "", "registry credential, or \"null\" for public pulls")
	completion.Flag(cmd, "registry", registriesOrNull)
	shared.Option(cmd, &opts.disk, "--disk <gb>", "", "container disk in GB, 20-2000 (requires stopped state)")
	shared.Option(cmd, &opts.command, "--command <string>", "", "start command, whitespace-split (requires stopped state)")
	shared.Repeatable(cmd, &opts.env, "--env <K=V>", "environment variable, repeatable — replaces the whole environment")
	shared.Repeatable(cmd, &opts.port, "--port <port|none>", "public port, repeatable — replaces every public port, applied at once; \"none\" removes them")
	shared.Option(cmd, &opts.profile, "--profile <name>", "", "profile to use")
	completion.Flag(cmd, "profile", shared.Profiles)

	return cmd
}
