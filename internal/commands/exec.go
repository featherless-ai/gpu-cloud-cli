package commands

import (
	"github.com/spf13/cobra"

	"featherless.ai/featherless-cloud/internal/api"
	"featherless.ai/featherless-cloud/internal/commands/shared"
	"featherless.ai/featherless-cloud/internal/completion"
	"featherless.ai/featherless-cloud/internal/errs"
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

// remoteExitCode is the exit status exec ends with: the command's own, 124
// for a timeout and 130 for a cancellation, as shells report them.
func remoteExitCode(job api.RunJob) int {
	return -1
}

func preemptionRetries(word string, ctx *completion.Context) ([]string, error) {
	return []string{"0", "1", "2", "3"}, nil
}

func newExecCommand() *cobra.Command {
	var opts execOptions

	cmd := &cobra.Command{
		Use:   "exec [command...]",
		Short: "Run a command on temporary CPU or GPU compute",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return errs.New("not implemented")
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
