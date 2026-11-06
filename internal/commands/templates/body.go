package templates

import (
	"context"

	"github.com/spf13/cobra"

	"featherless.ai/featherless-cloud/internal/api"
	"featherless.ai/featherless-cloud/internal/commands/shared"
	"featherless.ai/featherless-cloud/internal/completion"
	"featherless.ai/featherless-cloud/internal/errs"
	"featherless.ai/featherless-cloud/internal/output"
)

// templateFlags are the settings templates create and update take; a
// repeated flag replaces that whole list.
type templateFlags struct {
	description string
	visibility  string
	template    string
	image       string
	registry    string
	computeType string
	gpuType     string
	gpuCount    string
	cpuCores    string
	disk        string
	volume      []string
	command     string
	env         []string
	secret      []string
	port        []string
	tag         []string
}

func withTemplateFlags(cmd *cobra.Command, flags *templateFlags) {
	shared.Option(cmd, &flags.description, "--description <text>", "", "what the template is for")
	shared.Option(cmd, &flags.visibility, "--visibility <visibility>", "", "private, or organization to let every member launch it with its secrets")
	completion.Choices(cmd, "visibility", api.TemplateVisibilities...)
	shared.Option(cmd, &flags.template, "--template <name>", "", "start from an official template's image, command and environment; name:version for an older version")
	completion.Flag(cmd, "template", shared.Templates)
	shared.Option(cmd, &flags.image, "--image <reference>", "", "container image to run")
	shared.Option(cmd, &flags.registry, "--registry <name|id>", "", "registry credential for a private --image")
	completion.Flag(cmd, "registry", shared.Registries)
	shared.Option(cmd, &flags.computeType, "--compute-type <type>", "gpu", "gpu or cpu")
	completion.Choices(cmd, "compute-type", "gpu", "cpu")
	shared.Option(cmd, &flags.gpuType, "--gpu-type <text>", "", "GPU model substring, e.g. mi325x")
	completion.Flag(cmd, "gpu-type", shared.GPUTypes(api.WorkloadInstance))
	shared.Option(cmd, &flags.gpuCount, "--gpu-count <n>", "", "GPUs: 1, 2, 4, or 8")
	completion.Flag(cmd, "gpu-count", shared.GPUCounts(api.WorkloadInstance))
	shared.Option(cmd, &flags.cpuCores, "--cpu-cores <n>", "", "vCPU cores for --compute-type cpu")
	completion.Flag(cmd, "cpu-cores", shared.CPUCores(api.WorkloadInstance))
	shared.Option(cmd, &flags.disk, "--disk <gb>", "", "container disk in GB (20-2000)")
	shared.Repeatable(cmd, &flags.volume, "--volume <name:path>", "mount a network volume, repeatable")
	completion.Flag(cmd, "volume", shared.VolumeMounts)
	shared.Option(cmd, &flags.command, "--command <string>", "", "start command (whitespace-split, no shell quoting)")
	shared.Repeatable(cmd, &flags.env, "--env <K=V>", "environment variable, repeatable")
	shared.Repeatable(cmd, &flags.secret, "--secret <K=V>", "secret variable, repeatable; replaces the saved secrets")
	shared.Repeatable(cmd, &flags.port, "--port <port>", "serve this port at a public HTTPS address, repeatable")
	shared.Repeatable(cmd, &flags.tag, "--tag <tag>", "label such as AMD or \"CUDA 12.8\", repeatable; replaces the saved tags")
}

// templateBody holds only the flags that were typed, so an update changes nothing else.
func templateBody(ctx context.Context, client api.Cloud, flags templateFlags, cmd *cobra.Command, out *output.Context) (api.TemplateBody, error) {
	return api.TemplateBody{}, errs.New("not implemented")
}
