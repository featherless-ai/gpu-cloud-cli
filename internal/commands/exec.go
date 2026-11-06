package commands

import (
	"fmt"
	"os"
	"os/signal"
	"slices"
	"sync/atomic"
	"time"

	"github.com/spf13/cobra"

	"featherless.ai/featherless-cloud/internal/api"
	"featherless.ai/featherless-cloud/internal/commands/instance"
	"featherless.ai/featherless-cloud/internal/commands/job"
	"featherless.ai/featherless-cloud/internal/commands/shared"
	"featherless.ai/featherless-cloud/internal/completion"
	"featherless.ai/featherless-cloud/internal/errs"
	"featherless.ai/featherless-cloud/internal/output"
)

const (
	pollInterval      = 2 * time.Second
	maxReconnectDelay = 10 * time.Second
	finishedGrace     = 10 * time.Second
)

type execOptions struct {
	name              string
	template          string
	templateID        string
	templateVersion   string
	image             string
	registry          string
	gpuType           string
	gpuCount          string
	cpuCores          string
	disk              string
	volume            []string
	workdir           string
	env               []string
	secret            []string
	timeout           string
	preemptionRetries string
	noRetainLogs      bool
	idempotencyKey    string
	detach            bool
	cancelOnInterrupt bool
	profile           string
}

func boundedInteger(value, label string, low, high int) (int, error) {
	parsed := shared.ParseNumber(value)

	if !shared.IsInteger(parsed) || parsed < float64(low) || parsed > float64(high) {
		return 0, errs.Newf("%s must be between %d and %d.", label, low, high)
	}

	return int(parsed), nil
}

// remoteExitCode is the exit status exec ends with: the command's own, 124
// for a timeout and 130 for a cancellation, as shells report them.
func remoteExitCode(job api.RunJob) int {
	switch job.State {
	case api.JobSucceeded:
		if job.ExitCode != nil {
			return *job.ExitCode
		}

		return 0
	case api.JobTimedOut:
		return 124
	case api.JobCancelled:
		return 130
	}

	if job.ExitCode != nil {
		return *job.ExitCode
	}

	return 1
}

func preemptionRetries(word string, ctx *completion.Context) ([]string, error) {
	return []string{"0", "1", "2", "3"}, nil
}

func isTerminal(state api.RunJobState) bool {
	return slices.Contains(job.TerminalStates, state)
}

// attachment follows a submitted Run Job, printing its output until it ends.
type attachment struct {
	cmd               *cobra.Command
	ctx               *output.Context
	client            api.Cloud
	cancelOnInterrupt bool
	interrupted       atomic.Bool
}

func (a *attachment) status(message string) {
	fmt.Fprintln(a.ctx.Stderr, message)
}

// follow returns the job's last state and whether Ctrl-C ended the wait.
func (a *attachment) follow(initial api.RunJob) (api.RunJob, bool, error) {
	signals := make(chan os.Signal, 1)
	finished := make(chan struct{})
	signal.Notify(signals, os.Interrupt)
	defer signal.Stop(signals)
	defer close(finished)

	go func() {
		select {
		case <-signals:
			a.interrupted.Store(true)
			// Only the first Ctrl-C detaches; a second one ends the CLI.
			signal.Stop(signals)
		case <-finished:
		}
	}()

	current := initial
	var previousState api.RunJobState
	attempt := initial.AttemptNumber
	previousLogs := ""
	reconnectAttempts := 0
	isReconnecting := false
	var finishedAt time.Time

	for {
		done, wasInterrupted, err := a.step(&current, &previousState, &attempt, &previousLogs, &finishedAt)

		if err == nil && done {
			return current, wasInterrupted, nil
		}

		if err == nil {
			if isReconnecting {
				a.status("Run Job " + current.ShortID + ": connection restored.")
			}

			isReconnecting = false
			reconnectAttempts = 0

			continue
		}

		if !errs.IsRetryable(err) {
			return current, false, err
		}

		if !isReconnecting {
			a.status("Run Job " + current.ShortID + ": connection lost; reconnecting.")
			isReconnecting = true
		}

		if a.interrupted.Load() {
			a.status("Detached from Run Job " + current.ShortID + ". Reattach with featherless-cloud runjob logs " + current.ShortID + " --follow.")
			return current, true, nil
		}

		delay := min(time.Second<<reconnectAttempts, maxReconnectDelay)
		reconnectAttempts++
		time.Sleep(delay)
	}
}

// step prints what changed since the last poll and fetches the job again.
func (a *attachment) step(current *api.RunJob, previousState *api.RunJobState, attempt *int, previousLogs *string, finishedAt *time.Time) (bool, bool, error) {
	requestCtx := a.cmd.Context()

	if current.AttemptNumber != *attempt {
		a.status(fmt.Sprintf("Run Job %s: starting again from the beginning (attempt %d)", current.ShortID, current.AttemptNumber))
		*attempt = current.AttemptNumber
		*previousLogs = ""
	}

	if current.State != *previousState {
		a.status("Run Job " + current.ShortID + ": " + string(current.State))
		*previousState = current.State
	}

	isComplete := false

	if current.State == api.JobRunning || isTerminal(current.State) {
		latest, err := a.client.JobLogs(requestCtx, current.ID, 5000, api.SectionTail)
		if err != nil {
			return false, false, err
		}

		if delta := instance.NewContent(*previousLogs, latest.Logs); delta != "" {
			fmt.Fprint(a.ctx.Stdout, delta)
		}

		// Collection is asynchronous; an empty fetch does not reset the overlap.
		*previousLogs = instance.NextLogCursor(*previousLogs, latest.Logs, latest.Complete)
		isComplete = latest.Complete

		if latest.Truncated {
			a.status("Run Job " + current.ShortID + ": only recent output is shown; browse log history for earlier lines.")
		}
	}

	if isTerminal(current.State) && (isComplete || !current.RetainLogs) {
		return true, false, nil
	}

	if isTerminal(current.State) {
		if finishedAt.IsZero() {
			*finishedAt = a.ctx.Now()
		}

		if a.ctx.Now().Sub(*finishedAt) >= finishedGrace {
			a.status("Job " + current.ShortID + ": finished. More output may arrive; use featherless-cloud job logs " + current.ShortID + " to check again.")
			return true, false, nil
		}
	}

	if a.interrupted.Load() {
		if a.cancelOnInterrupt {
			if _, err := a.client.CancelJob(requestCtx, current.ID); err != nil {
				return false, false, err
			}

			a.status("Run Job " + current.ShortID + ": cancellation requested.")
		} else {
			a.status("Detached from Run Job " + current.ShortID + ". Reattach with featherless-cloud runjob logs " + current.ShortID + " --follow.")
		}

		return true, true, nil
	}

	time.Sleep(pollInterval)

	fetched, err := a.client.GetJob(requestCtx, current.ID)
	if err != nil {
		return false, false, err
	}

	*current = fetched

	return false, false, nil
}

// executionOptions is what exec looks up before building the body.
type executionOptions struct {
	found       *shared.TemplateChoice
	pricing     []api.PriceRate
	volumes     []api.NetworkVolume
	credentials []api.RegistryCredential
}

// loadExecutionOptions makes every lookup, as the TypeScript CLI started
// them all at once, and returns the first failure.
func loadExecutionOptions(cmd *cobra.Command, client api.Cloud, opts *execOptions) (executionOptions, error) {
	var loaded executionOptions
	var failures []error
	templates := shared.TemplateOptions{Template: opts.template, TemplateID: opts.templateID, TemplateVersion: opts.templateVersion}

	found, err := shared.FindTemplate(cmd.Context(), client, templates)
	loaded.found = found
	failures = append(failures, err)

	loaded.pricing, err = client.Pricing(cmd.Context(), api.WorkloadRunJob)
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

func changedValue(cmd *cobra.Command, name, value string) *string {
	if !cmd.Flags().Changed(name) {
		return nil
	}

	return &value
}

func mountsFor(entries []string, volumes []api.NetworkVolume) ([]api.NetworkVolumeMount, error) {
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

func variablesOf(values []string, label string) ([]api.EnvVar, error) {
	pairs, err := shared.ParseRepeatable(values, label)
	if err != nil {
		return nil, err
	}

	return shared.ToEnvironment(pairs)
}

func execBody(cmd *cobra.Command, ctx *output.Context, command []string, opts *execOptions, loaded executionOptions, name string) (api.CreateRunJobBody, error) {
	body := api.CreateRunJobBody{Name: name}
	var template *api.Template

	if loaded.found != nil {
		template = &loaded.found.Template
	}

	if len(command) == 0 && (template == nil || len(template.Command) == 0) {
		return body, errs.New("Provide the command after --.")
	}

	// Without a template or image the server runs the PyTorch environment.
	var image shared.ImageFields

	if opts.image != "" || opts.registry != "" {
		imageOptions := shared.ImageOptions{Image: opts.image, Registry: opts.registry}
		resolved, err := shared.ResolveImage(loaded.credentials, imageOptions, template != nil)

		if err != nil {
			return body, err
		}

		image = resolved
	}

	// A template's compute stands unless a flag says otherwise.
	hasTemplateCompute := template != nil && template.ComputeSKU != nil && *template.ComputeSKU != ""
	usesTemplateCompute := hasTemplateCompute && opts.gpuType == "" && opts.gpuCount == "" && opts.cpuCores == ""

	if !usesTemplateCompute {
		computeType := api.ComputeGPU

		if opts.cpuCores != "" {
			computeType = api.ComputeCPU
		}

		compute := shared.ComputeOptions{
			ComputeType: computeType,
			GPUType:     changedValue(cmd, "gpu-type", opts.gpuType),
			GPUCount:    changedValue(cmd, "gpu-count", opts.gpuCount),
			CPUCores:    changedValue(cmd, "cpu-cores", opts.cpuCores),
		}

		sku, err := shared.ResolveCompute(loaded.pricing, compute, ctx)
		if err != nil {
			return body, err
		}

		body.ComputeSKU = &sku
	}

	mounts, err := mountsFor(opts.volume, loaded.volumes)
	if err != nil {
		return body, err
	}

	if template != nil {
		body.TemplateID = &template.ID
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

	if len(command) > 0 {
		body.Command = command
	}

	if template == nil || cmd.Flags().Changed("disk") {
		disk, err := shared.ParseDisk(opts.disk)
		if err != nil {
			return body, err
		}

		body.EphemeralDiskGiB = &disk
	}

	body.WorkingDirectory = &opts.workdir

	timeout, err := boundedInteger(opts.timeout, "Timeout", 1, 86_400)
	if err != nil {
		return body, err
	}

	retries, err := boundedInteger(opts.preemptionRetries, "Preemption retries", 0, 3)
	if err != nil {
		return body, err
	}

	retainLogs := !opts.noRetainLogs
	body.TimeoutSeconds = &timeout
	body.PreemptionRetries = &retries
	body.RetainLogs = &retainLogs

	if len(mounts) > 0 {
		body.NetworkVolumeMounts = mounts
	}

	environment, err := variablesOf(opts.env, "env")
	if err != nil {
		return body, err
	}

	if len(environment) > 0 {
		body.Environment = environment
	}

	secrets, err := variablesOf(opts.secret, "secret")
	if err != nil {
		return body, err
	}

	if len(secrets) > 0 {
		body.Secrets = secrets
	}

	return body, nil
}

func runExec(cmd *cobra.Command, command []string, opts *execOptions) error {
	if opts.cpuCores != "" && (opts.gpuCount != "" || opts.gpuType != "") {
		return errs.New("--cpu-cores cannot be combined with GPU options.")
	}

	ctx := shared.NewContext(cmd)

	if ctx.Mode == output.JSON && !opts.detach {
		return errs.New("--json can only be used with --detach; attached stdout is reserved for command output.")
	}

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

	loaded, err := output.Spin(ctx, "Loading execution options", func() (executionOptions, error) {
		return loadExecutionOptions(cmd, client, opts)
	})

	if err != nil {
		return err
	}

	body, err := execBody(cmd, ctx, command, opts, loaded, name)
	if err != nil {
		return err
	}

	key := opts.idempotencyKey

	if !cmd.Flags().Changed("idempotency-key") {
		key = shared.NewIdempotencyKey()
	}

	submitted, err := output.Spin(ctx, "Starting command", func() (api.RunJob, error) {
		return client.CreateJob(cmd.Context(), body, key)
	})

	if err != nil {
		return err
	}

	if opts.detach {
		ctx.Output(submitted, func() output.Result { return output.Result{Text: submitted.ShortID} })
		return nil
	}

	attached := &attachment{cmd: cmd, ctx: ctx, client: client, cancelOnInterrupt: opts.cancelOnInterrupt}
	completed, wasInterrupted, err := attached.follow(submitted)

	if err != nil {
		return err
	}

	code := 0

	if wasInterrupted {
		code = 130
	}

	if isTerminal(completed.State) {
		code = remoteExitCode(completed)
	}

	if code != 0 {
		return &errs.Exit{Code: code}
	}

	return nil
}

func newExecCommand() *cobra.Command {
	var opts execOptions

	cmd := &cobra.Command{
		Use:   "exec [command...]",
		Short: "Run a command on temporary CPU or GPU compute",
		Args:  shared.Arguments,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runExec(cmd, args, &opts)
		},
	}

	completion.Args(cmd, completion.Arg{Name: "command", Variadic: true})
	shared.Option(cmd, &opts.name, "--name <name>", "", "Run Job name (random when omitted)")
	shared.Option(cmd, &opts.template, "--template <name>", "", "template: a name, org/<name>, featherless/<name> or an id; defaults to the PyTorch environment")
	completion.Flag(cmd, "template", shared.Templates)
	shared.Option(cmd, &opts.templateID, "--template-id <id>", "", "template, by id")
	completion.Flag(cmd, "template-id", shared.TemplateIDs)
	shared.Option(cmd, &opts.templateVersion, "--template-version <version>", "", "an official template's version, e.g. 2.11.0 (or --template name:version); its current one by default")
	shared.Option(cmd, &opts.image, "--image <reference>", "", "container image to run, or to replace the template's")
	shared.Option(cmd, &opts.registry, "--registry <name|id>", "", "registry credential for a private image")
	completion.Flag(cmd, "registry", shared.Registries)
	shared.Option(cmd, &opts.gpuType, "--gpu-type <text>", "", "GPU model substring, e.g. mi325x")
	completion.Flag(cmd, "gpu-type", shared.GPUTypes(api.WorkloadRunJob))
	shared.Option(cmd, &opts.gpuCount, "--gpu-count <n>", "", "GPUs: 1, 2, 4, or 8")
	completion.Flag(cmd, "gpu-count", shared.GPUCounts(api.WorkloadRunJob))
	shared.Option(cmd, &opts.cpuCores, "--cpu-cores <n>", "", "vCPU cores; mutually exclusive with GPU options")
	completion.Flag(cmd, "cpu-cores", shared.CPUCores(api.WorkloadRunJob))
	shared.Option(cmd, &opts.disk, "--disk <gb>", "20", "container disk in GB (20-2000)")
	shared.Repeatable(cmd, &opts.volume, "--volume <name:path>", "mount a network volume, repeatable")
	completion.Flag(cmd, "volume", shared.VolumeMounts)
	shared.Option(cmd, &opts.workdir, "--workdir <path>", "/workspace", "container working directory")
	shared.Repeatable(cmd, &opts.env, "--env <K=V>", "environment variable, repeatable")
	shared.Repeatable(cmd, &opts.secret, "--secret <K=V>", "secret variable, repeatable")
	shared.Option(cmd, &opts.timeout, "--timeout <seconds>", "86400", "run time limit after compute assignment (1-86400)")
	shared.Option(cmd, &opts.preemptionRetries, "--preemption-retries <n>", "0", "start again from the beginning up to n times if interrupted for other work (0-3)")
	completion.Flag(cmd, "preemption-retries", preemptionRetries)
	shared.Switch(cmd, &opts.noRetainLogs, "--no-retain-logs", "disable output collection, including live attached output")
	shared.Option(cmd, &opts.idempotencyKey, "--idempotency-key <key>", "", "retry-safe execution key (random when omitted)")
	shared.Switch(cmd, &opts.detach, "--detach", "submit without waiting for the result")
	shared.Switch(cmd, &opts.cancelOnInterrupt, "--cancel-on-interrupt", "cancel instead of detach when Ctrl-C is pressed")
	shared.Option(cmd, &opts.profile, "--profile <name>", "", "authentication profile to use")
	completion.Flag(cmd, "profile", shared.Profiles)
	shared.HelpAfter(cmd, "\nExamples:\n  featherless-cloud exec --gpu-count 1 -- python -c 'import torch; print(torch.cuda.is_available())'\n  featherless-cloud exec --cpu-cores 32 -- python -c 'import os; print(os.cpu_count())'")

	return cmd
}
