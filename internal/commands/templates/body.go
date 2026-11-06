package templates

import (
	"strings"

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

func changed(cmd *cobra.Command, name, value string) *string {
	if !cmd.Flags().Changed(name) {
		return nil
	}

	return &value
}

func variables(values []string, label string) ([]api.EnvVar, error) {
	pairs, err := shared.ParseRepeatable(values, label)
	if err != nil {
		return nil, err
	}

	return shared.ToEnvironment(pairs)
}

// templateBody holds only the flags that were typed, so an update changes nothing else.
func templateBody(client api.Cloud, flags templateFlags, cmd *cobra.Command, ctx *output.Context) (api.TemplateBody, error) {
	var body api.TemplateBody
	requestCtx := cmd.Context()

	if flags.template != "" && flags.image != "" {
		return body, errs.New("Choose either --template or --image.")
	}

	if cmd.Flags().Changed("description") {
		body.Description = &flags.description
	}

	if flags.visibility != "" {
		visibility := api.TemplateVisibility(flags.visibility)
		body.Visibility = &visibility
	}

	if flags.template != "" {
		ref, version := shared.SplitTemplateVersion(flags.template)

		templates, err := client.Templates(requestCtx)
		if err != nil {
			return body, err
		}

		var official []api.Template

		for _, template := range templates {
			if template.Official {
				official = append(official, template)
			}
		}

		base, err := shared.ResolveTemplateRef(official, ref)
		if err != nil {
			return body, err
		}

		body.BaseTemplateID = &base.ID

		checked, err := shared.CheckTemplateVersion(base, version)
		if err != nil {
			return body, err
		}

		if checked != "" {
			body.BaseTemplateVersion = &checked
		}
	}

	if flags.image != "" {
		image := strings.TrimSpace(flags.image)
		body.ContainerImage = &image
	}

	if flags.registry != "" {
		credentials, err := client.RegistryCredentials(requestCtx)
		if err != nil {
			return body, err
		}

		id, err := shared.ResolveCredentialRef(credentials, flags.registry)
		if err != nil {
			return body, err
		}

		body.RegistryCredentialID = api.Some(id)
	}

	if shared.ComputeFlagsGiven(cmd) {
		prices, err := client.Pricing(requestCtx, api.WorkloadInstance)
		if err != nil {
			return body, err
		}

		compute := shared.ComputeOptions{
			ComputeType: api.ComputeType(flags.computeType),
			GPUType:     changed(cmd, "gpu-type", flags.gpuType),
			GPUCount:    changed(cmd, "gpu-count", flags.gpuCount),
			CPUCores:    changed(cmd, "cpu-cores", flags.cpuCores),
		}

		sku, err := shared.ResolveCompute(prices, compute, ctx)
		if err != nil {
			return body, err
		}

		body.ComputeSKU = api.Some(sku)
	}

	if cmd.Flags().Changed("disk") {
		disk, err := shared.ParseDisk(flags.disk)
		if err != nil {
			return body, err
		}

		body.EphemeralDiskGiB = api.Some(disk)
	}

	if len(flags.volume) > 0 {
		volumes, err := client.ListVolumes(requestCtx)
		if err != nil {
			return body, err
		}

		entries, err := shared.ParseVolumeFlags(flags.volume)
		if err != nil {
			return body, err
		}

		mounts := []api.NetworkVolumeMount{}

		for _, entry := range entries {
			id, err := shared.ResolveVolume(volumes, entry.Volume)
			if err != nil {
				return body, err
			}

			mounts = append(mounts, api.NetworkVolumeMount{NetworkVolumeID: id, MountPath: entry.MountPath})
		}

		if err := shared.ValidateMounts(mounts); err != nil {
			return body, err
		}

		body.NetworkVolumeMounts = mounts
	}

	if cmd.Flags().Changed("command") {
		body.Command = strings.Fields(flags.command)

		if body.Command == nil {
			body.Command = []string{}
		}
	}

	if len(flags.env) > 0 {
		environment, err := variables(flags.env, "env")
		if err != nil {
			return body, err
		}

		body.Environment = environment
	}

	if len(flags.secret) > 0 {
		secrets, err := variables(flags.secret, "secret")
		if err != nil {
			return body, err
		}

		body.Secrets = secrets
	}

	if len(flags.port) > 0 {
		ports, err := shared.ParsePorts(flags.port)
		if err != nil {
			return body, err
		}

		body.PublicPorts = ports
	}

	if len(flags.tag) > 0 {
		body.Tags = flags.tag
	}

	return body, nil
}
