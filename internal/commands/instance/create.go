package instance

import (
	"strings"

	"github.com/spf13/cobra"

	"featherless.ai/featherless-cloud/internal/api"
	"featherless.ai/featherless-cloud/internal/commands/shared"
	"featherless.ai/featherless-cloud/internal/completion"
	"featherless.ai/featherless-cloud/internal/errs"
	"featherless.ai/featherless-cloud/internal/output"
	"featherless.ai/featherless-cloud/internal/project"
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
	project         shared.ProjectOptions
}

// changed is the flag's value when it was typed, else nil.
func changed(cmd *cobra.Command, name, value string) *string {
	if !cmd.Flags().Changed(name) {
		return nil
	}

	return &value
}

// configurator is what instance create looks up before building the body.
type configurator struct {
	found       *shared.TemplateChoice
	pricing     []api.PriceRate
	volumes     []api.NetworkVolume
	credentials []api.RegistryCredential
}

// loadConfigurator makes every lookup, as the TypeScript CLI started them
// all at once, and returns the first failure.
func loadConfigurator(cmd *cobra.Command, client api.Cloud, opts *createOptions) (configurator, error) {
	var loaded configurator
	var failures []error
	templates := shared.TemplateOptions{Template: opts.template, TemplateID: opts.templateID, TemplateVersion: opts.templateVersion}

	found, err := shared.FindTemplate(cmd.Context(), client, templates)
	loaded.found = found
	failures = append(failures, err)

	loaded.pricing, err = client.Pricing(cmd.Context(), api.WorkloadInstance)
	failures = append(failures, err)

	if len(opts.volume) > 0 {
		loaded.volumes, err = client.ListVolumes(cmd.Context())
		failures = append(failures, err)
	}

	if opts.registry != "" {
		loaded.credentials, err = client.RegistryCredentials(cmd.Context())
		failures = append(failures, err)
	}

	return loaded, shared.FirstError(failures...)
}

func resolveMounts(entries []string, volumes []api.NetworkVolume) ([]api.NetworkVolumeMount, error) {
	flags, err := shared.ParseVolumeFlags(entries)
	if err != nil {
		return nil, err
	}

	mounts := []api.NetworkVolumeMount{}

	for _, entry := range flags {
		id, err := shared.ResolveVolume(volumes, entry.Volume)
		if err != nil {
			return nil, err
		}

		mounts = append(mounts, api.NetworkVolumeMount{NetworkVolumeID: id, MountPath: entry.MountPath})
	}

	if len(mounts) > 8 {
		return nil, errs.New("At most 8 network volumes can be mounted.")
	}

	if err := shared.ValidateMounts(mounts); err != nil {
		return nil, err
	}

	return mounts, nil
}

// variables reads repeated --env or --secret values.
func variables(values []string, label string) ([]api.EnvVar, error) {
	pairs, err := shared.ParseRepeatable(values, label)
	if err != nil {
		return nil, err
	}

	return shared.ToEnvironment(pairs)
}

func createInstance(cmd *cobra.Command, opts *createOptions) error {
	if err := shared.ApplyProject(cmd, opts.project, project.BlockInstance); err != nil {
		return err
	}

	ctx := shared.NewContext(cmd)

	client, err := shared.Client(opts.profile)
	if err != nil {
		return err
	}

	name := opts.name

	if !cmd.Flags().Changed("name") {
		name = shared.RandomInstanceName()
	}

	if err := shared.AssertInstanceName(name); err != nil {
		return err
	}

	loaded, err := loadConfigurator(cmd, client, opts)

	if err != nil {
		return err
	}

	var chosen *api.Template

	if loaded.found != nil {
		chosen = &loaded.found.Template
	}

	var image shared.ImageFields
	shouldAskForImage := chosen == nil && opts.image == "" && ctx.Mode == output.Interactive

	if shouldAskForImage {
		templates, err := client.Templates(cmd.Context())

		if err != nil {
			return err
		}

		selection, err := shared.SelectImage(templates, ctx)
		if err != nil {
			return err
		}

		chosen = selection.Template
		image.ContainerImage = selection.ContainerImage
	} else {
		imageOptions := shared.ImageOptions{Image: opts.image, Registry: opts.registry}

		if image, err = shared.ResolveImage(loaded.credentials, imageOptions, chosen != nil); err != nil {
			return err
		}
	}

	body := api.CreateInstanceBody{Name: name}

	if chosen != nil {
		body.TemplateID = &chosen.ID
	}

	if loaded.found != nil && loaded.found.Version != "" {
		body.TemplateVersion = &loaded.found.Version
	}

	if image.ContainerImage != "" {
		body.ContainerImage = &image.ContainerImage
	}

	if image.RegistryCredentialID != "" {
		body.RegistryCredentialID = api.Some(image.RegistryCredentialID)
	}

	// A template's compute and disk stand unless a flag says otherwise.
	usesTemplateCompute := chosen != nil && chosen.ComputeSKU != nil && *chosen.ComputeSKU != ""

	if !usesTemplateCompute || shared.ComputeFlagsGiven(cmd) {
		compute := shared.ComputeOptions{
			ComputeType: api.ComputeType(opts.computeType),
			GPUType:     changed(cmd, "gpu-type", opts.gpuType),
			GPUCount:    changed(cmd, "gpu-count", opts.gpuCount),
			CPUCores:    changed(cmd, "cpu-cores", opts.cpuCores),
			Interactive: ctx.Mode == output.Interactive,
		}

		sku, err := shared.ResolveCompute(loaded.pricing, compute, ctx)
		if err != nil {
			return err
		}

		body.ComputeSKU = &sku
	}

	if chosen == nil || cmd.Flags().Changed("disk") {
		disk, err := shared.ParseDisk(opts.disk)
		if err != nil {
			return err
		}

		body.EphemeralDiskGiB = &disk
	}

	mounts, err := resolveMounts(opts.volume, loaded.volumes)
	if err != nil {
		return err
	}

	quota, err := client.Organization(cmd.Context())
	isAtGPULimit := err == nil && quota.GPULimit > 0 && quota.GPUCount >= quota.GPULimit

	if isAtGPULimit {
		ctx.Warn("Organization GPU limit already reached; the server may reject this.")
	}

	if len(mounts) > 0 {
		body.NetworkVolumeMounts = mounts
	}

	if command := strings.TrimSpace(opts.command); command != "" {
		body.Command = strings.Fields(command)
	}

	environment, err := variables(opts.env, "env")
	if err != nil {
		return err
	}

	if len(environment) > 0 {
		body.Environment = environment
	}

	secrets, err := variables(opts.secret, "secret")
	if err != nil {
		return err
	}

	if len(secrets) > 0 {
		body.Secrets = secrets
	}

	ports, err := shared.ParsePorts(opts.port)
	if err != nil {
		return err
	}

	if len(ports) > 0 {
		body.PublicPorts = ports
	}

	instance, err := client.CreateInstance(cmd.Context(), body, shared.NewIdempotencyKey())

	if err != nil {
		return err
	}

	ctx.Success("Instance " + instance.Name + " (" + instance.ShortID + ") queued. Track it with featherless-cloud instance get " + instance.ShortID + ".")
	ctx.Output(instance, func() output.Result { return output.Result{Text: "STATE " + string(instance.State)} })

	return nil
}

func newCreateCommand() *cobra.Command {
	var opts createOptions

	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create an instance",
		Args:  shared.Arguments,
		RunE: func(cmd *cobra.Command, args []string) error {
			return createInstance(cmd, &opts)
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
	shared.ProjectFlags(cmd, &opts.project)
	shared.HelpAfter(cmd, "\nCreation is retry-safe: re-running the same command within the\nidempotency window returns the original instance.")

	return cmd
}
