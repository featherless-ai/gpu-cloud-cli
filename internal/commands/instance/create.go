package instance

import (
	"github.com/spf13/cobra"

	"featherless.ai/featherless-cloud/internal/api"
	"featherless.ai/featherless-cloud/internal/commands/shared"
	"featherless.ai/featherless-cloud/internal/completion"
	"featherless.ai/featherless-cloud/internal/errs"
)

type createOptions struct {
	name            string
	template        string
	templateID      string
	templateVersion string
	image           string
	registry        string
	computeType     string
	gpuType         string
	gpuCount        string
	cpuCores        string
	disk            string
	volume          []string
	command         string
	env             []string
	secret          []string
	port            []string
	profile         string
}

func newCreateCommand() *cobra.Command {
	var opts createOptions

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create an instance",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return errs.New("not implemented")
		},
	}

	shared.Option(cmd, &opts.name, "--name <name>", "", "instance name (random when omitted)")
	shared.Option(cmd, &opts.template, "--template <name>", "", "template to launch: a name, org/<name>, featherless/<name> or an id; other flags override it")
	completion.Flag(cmd, "template", shared.Templates)
	shared.Option(cmd, &opts.templateID, "--template-id <id>", "", "template to launch, by id")
	completion.Flag(cmd, "template-id", shared.TemplateIDs)
	shared.Option(cmd, &opts.templateVersion, "--template-version <version>", "", "an official template's version, e.g. 2.11.0 (or --template name:version); its current one by default")
	shared.Option(cmd, &opts.image, "--image <reference>", "", "container image to run, or to replace the template's")
	shared.Option(cmd, &opts.registry, "--registry <name|id>", "", "registry credential for a private image")
	completion.Flag(cmd, "registry", shared.Registries)
	shared.Option(cmd, &opts.computeType, "--compute-type <type>", "gpu", "gpu or cpu")
	completion.Choices(cmd, "compute-type", "gpu", "cpu")
	shared.Option(cmd, &opts.gpuType, "--gpu-type <text>", "", "GPU model substring, e.g. mi325x")
	completion.Flag(cmd, "gpu-type", shared.GPUTypes(api.WorkloadInstance))
	shared.Option(cmd, &opts.gpuCount, "--gpu-count <n>", "", "GPUs per instance: 1, 2, 4, or 8")
	completion.Flag(cmd, "gpu-count", shared.GPUCounts(api.WorkloadInstance))
	shared.Option(cmd, &opts.cpuCores, "--cpu-cores <n>", "", "vCPU cores for --compute-type cpu")
	completion.Flag(cmd, "cpu-cores", shared.CPUCores(api.WorkloadInstance))
	shared.Option(cmd, &opts.disk, "--disk <gb>", "20", "container disk in GB (20-2000)")
	shared.Repeatable(cmd, &opts.volume, "--volume <name:path>", "mount a network volume, repeatable")
	completion.Flag(cmd, "volume", shared.VolumeMounts)
	shared.Option(cmd, &opts.command, "--command <string>", "", "override the start command (whitespace-split, no shell quoting)")
	shared.Repeatable(cmd, &opts.env, "--env <K=V>", "environment variable, repeatable")
	shared.Repeatable(cmd, &opts.secret, "--secret <K=V>", "secret variable, repeatable")
	shared.Repeatable(cmd, &opts.port, "--port <port>", "serve this port at a public HTTPS address, repeatable")
	shared.Option(cmd, &opts.profile, "--profile <name>", "", "profile to use")
	completion.Flag(cmd, "profile", shared.Profiles)
	shared.HelpAfter(cmd, "\nCreation is retry-safe: re-running the same command within the\nidempotency window returns the original instance.")

	return cmd
}
