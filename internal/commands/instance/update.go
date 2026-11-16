package instance

import (
	"strings"

	"github.com/spf13/cobra"

	"featherless.ai/featherless-cloud/internal/api"
	"featherless.ai/featherless-cloud/internal/commands/shared"
	"featherless.ai/featherless-cloud/internal/completion"
	"featherless.ai/featherless-cloud/internal/errs"
	"featherless.ai/featherless-cloud/internal/output"
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
	names, err := shared.Registries(word, ctx)
	if err != nil {
		return nil, err
	}

	if strings.HasPrefix("null", word) {
		names = append(names, "null")
	}

	return names, nil
}

func updateBody(cmd *cobra.Command, client api.Cloud, opts *updateOptions) (api.UpdateInstanceBody, bool, error) {
	var body api.UpdateInstanceBody
	flags := cmd.Flags()
	hasChanges := false

	if flags.Changed("name") {
		if err := shared.AssertInstanceName(opts.name); err != nil {
			return body, false, err
		}

		body.Name = &opts.name
		hasChanges = true
	}

	if flags.Changed("image") {
		reference := strings.TrimSpace(opts.image)

		if shared.UTF16Length(reference) < 3 {
			return body, false, errs.New("Enter a container image.")
		}

		body.ContainerImage = &reference
		hasChanges = true
	}

	if flags.Changed("registry") {
		if opts.registry == "null" {
			body.RegistryCredentialID = api.Null[string]()
		} else {
			credentials, err := client.RegistryCredentials(cmd.Context())
			if err != nil {
				return body, false, err
			}

			id, err := shared.ResolveCredentialRef(credentials, opts.registry)
			if err != nil {
				return body, false, err
			}

			body.RegistryCredentialID = api.Some(id)
		}

		hasChanges = true
	}

	if flags.Changed("disk") {
		disk, err := shared.ParseDisk(opts.disk)
		if err != nil {
			return body, false, err
		}

		body.EphemeralDiskGiB = &disk
		hasChanges = true
	}

	if flags.Changed("command") {
		body.Command = strings.Fields(opts.command)

		if body.Command == nil {
			body.Command = []string{}
		}

		hasChanges = true
	}

	if len(opts.env) > 0 {
		environment, err := variables(opts.env, "env")
		if err != nil {
			return body, false, err
		}

		body.Environment = environment
		hasChanges = true
	}

	if len(opts.port) > 0 {
		ports, err := shared.ParsePorts(opts.port)
		if err != nil {
			return body, false, err
		}

		body.PublicPorts = ports
		hasChanges = true
	}

	return body, hasChanges, nil
}

func newUpdateCommand() *cobra.Command {
	var opts updateOptions

	cmd := &cobra.Command{
		Use:   "update <id>",
		Short: "Update an instance (config fields require a stopped instance)",
		Args:  shared.Arguments,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := shared.NewContext(cmd)

			client, err := shared.Client(opts.profile)
			if err != nil {
				return err
			}

			id, err := shared.ResolveInstanceID(cmd.Context(), client, args[0])
			if err != nil {
				return err
			}

			body, hasChanges, err := updateBody(cmd, client, &opts)
			if err != nil {
				return err
			}

			if !hasChanges {
				return ctx.Fatal("Nothing to update.")
			}

			instance, err := client.PatchInstance(cmd.Context(), id, body)

			if err != nil {
				return err
			}

			ctx.Success("Instance " + instance.Name + " (" + instance.ShortID + ") updated.")
			ctx.Output(instance, func() output.Result { return output.Result{Text: "STATE " + string(instance.State)} })

			return nil
		},
	}

	completion.Args(cmd, completion.Arg{Name: "id", Description: "instance name or short id", Complete: shared.Instances})
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
