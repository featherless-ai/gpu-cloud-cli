package commands

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"featherless.ai/featherless-cloud/internal/api"
	"featherless.ai/featherless-cloud/internal/commands/shared"
	"featherless.ai/featherless-cloud/internal/commands/volume"
	"featherless.ai/featherless-cloud/internal/completion"
)

// fakeCloud answers only the calls a test sets; any other call panics.
type fakeCloud struct {
	api.Cloud
	registryCredentials func() ([]api.RegistryCredential, error)
	getJob              func(id string) (api.RunJob, error)
	pricing             func(workload api.Workload) ([]api.PriceRate, error)
}

func (f *fakeCloud) RegistryCredentials(ctx context.Context) ([]api.RegistryCredential, error) {
	return f.registryCredentials()
}

func (f *fakeCloud) GetJob(ctx context.Context, id string) (api.RunJob, error) {
	return f.getJob(id)
}

func (f *fakeCloud) Pricing(ctx context.Context, workload api.Workload) ([]api.PriceRate, error) {
	return f.pricing(workload)
}

func completerContext(cloud api.Cloud, args ...string) *completion.Context {
	return &completion.Context{
		Args:  args,
		Cloud: func() (api.Cloud, error) { return cloud, nil },
	}
}

// sameCompleter reports whether got is the completer function want.
func sameCompleter(got, want completion.Completer) bool {
	if got == nil || want == nil {
		return got == nil && want == nil
	}

	return reflect.ValueOf(got).Pointer() == reflect.ValueOf(want).Pointer()
}

// declared looks up the completers the real command tree declares.
type declared struct {
	t    *testing.T
	root *cobra.Command
}

func newDeclared(t *testing.T) declared {
	return declared{t: t, root: NewRoot()}
}

func (d declared) command(path string) *cobra.Command {
	d.t.Helper()
	words := strings.Fields(path)
	cmd, rest, err := d.root.Find(words)
	last := words[len(words)-1]

	if err != nil || len(rest) != 0 || (cmd.Name() != last && !cmd.HasAlias(last)) {
		d.t.Fatalf("no command %q", path)
	}

	return cmd
}

// argument is the completer of the command's first positional argument.
func (d declared) argument(path string) completion.Completer {
	d.t.Helper()
	args := completion.ArgsOf(d.command(path))

	if len(args) == 0 {
		return nil
	}

	return completion.ArgCompleter(args[0])
}

func (d declared) option(path, flag string) completion.Completer {
	d.t.Helper()
	cmd := d.command(path)
	name := strings.TrimPrefix(flag, "--")
	found := cmd.Flags().Lookup(name)

	if found == nil {
		found = cmd.InheritedFlags().Lookup(name)
	}

	if found == nil {
		return nil
	}

	return completion.FlagCompleter(found)
}

// values completes the empty word.
func values(t *testing.T, complete completion.Completer, ctx *completion.Context) []string {
	t.Helper()

	if complete == nil {
		t.Errorf("no completer")
		return nil
	}

	got, err := complete("", ctx)

	if err != nil {
		t.Errorf("completer failed: %v", err)
	}

	return got
}

func wantValues(t *testing.T, label string, got, want []string) {
	t.Helper()

	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s = %q, want %q", label, got, want)
	}
}

func TestDeclaredCompletersCompletesItsArgument(t *testing.T) {
	d := newDeclared(t)
	cases := map[string]completion.Completer{}

	for _, name := range []string{"get", "update", "logs", "history", "shell", "ssh", "tmux", "start", "stop", "restart", "delete"} {
		cases["instance "+name] = shared.Instances
	}

	for _, name := range []string{"get", "logs", "cancel", "delete"} {
		cases["job "+name] = shared.Jobs
	}

	for _, name := range []string{"get", "update", "delete"} {
		cases["volume "+name] = shared.Volumes
	}

	for _, name := range []string{"ls", "find", "mkdir", "rm", "cp", "mv"} {
		cases["volume "+name] = volume.Paths
	}

	for _, name := range []string{"show", "update", "delete"} {
		cases["templates "+name] = shared.Templates
	}

	for command, want := range cases {
		t.Run(command, func(t *testing.T) {
			if !sameCompleter(d.argument(command), want) {
				t.Errorf("%s does not complete its argument with the expected completer", command)
			}
		})
	}
}

func TestDeclaredCompletersCompletesOptionValues(t *testing.T) {
	d := newDeclared(t)

	for _, command := range []string{"instance create", "exec"} {
		options := map[string]completion.Completer{
			"--template":    shared.Templates,
			"--template-id": shared.TemplateIDs,
			"--registry":    shared.Registries,
			"--volume":      shared.VolumeMounts,
		}

		for flag, want := range options {
			if !sameCompleter(d.option(command, flag), want) {
				t.Errorf("%s %s has another completer", command, flag)
			}
		}
	}

	for _, command := range []string{"login", "instance list", "instance ssh", "job logs", "volume cp", "pricing list", "exec"} {
		if !sameCompleter(d.option(command, "--profile"), shared.Profiles) {
			t.Errorf("%s --profile does not complete profiles", command)
		}
	}

	cloud := &fakeCloud{
		registryCredentials: func() ([]api.RegistryCredential, error) {
			return []api.RegistryCredential{{Name: "ghcr"}}, nil
		},
	}

	got := values(t, d.option("instance update", "--registry"), completerContext(cloud))
	wantValues(t, "instance update --registry", got, []string{"ghcr", "null"})
}

func TestDeclaredCompletersCompletesRecordedAttemptsOfTheJobNamedByTheFirstArgument(t *testing.T) {
	d := newDeclared(t)
	var requested []string

	cloud := &fakeCloud{
		getJob: func(id string) (api.RunJob, error) {
			requested = append(requested, id)
			attempts := []api.RunJobAttempt{
				{Number: 1, ContainerRunID: new("a")},
				{Number: 2, ContainerRunID: nil},
				{Number: 3, ContainerRunID: new("c")},
			}

			return api.RunJob{Attempts: attempts}, nil
		},
	}

	attempt := d.option("job logs", "--attempt")

	got := values(t, attempt, completerContext(cloud, "vgjggsynrr"))
	wantValues(t, "attempts", got, []string{"1", "3"})

	if !reflect.DeepEqual(requested, []string{"vgjggsynrr"}) {
		t.Errorf("fetched %q, want vgjggsynrr", requested)
	}

	got = values(t, attempt, completerContext(cloud))

	if len(got) != 0 {
		t.Errorf("without a job id = %q, want nothing", got)
	}

	if len(requested) != 1 {
		t.Errorf("fetched %d jobs, want 1", len(requested))
	}
}

func TestDeclaredCompletersCompletesComputeOptionsFromTheWorkloadTheCommandRuns(t *testing.T) {
	d := newDeclared(t)
	var lastWorkload api.Workload

	cloud := &fakeCloud{
		pricing: func(workload api.Workload) ([]api.PriceRate, error) {
			lastWorkload = workload
			return []api.PriceRate{}, nil
		},
	}

	cases := []struct {
		command  string
		workload api.Workload
	}{
		{"instance create", api.WorkloadInstance},
		{"exec", api.WorkloadRunJob},
	}

	for _, c := range cases {
		for _, flag := range []string{"--gpu-type", "--gpu-count", "--cpu-cores"} {
			lastWorkload = ""
			values(t, d.option(c.command, flag), completerContext(cloud))

			if lastWorkload != c.workload {
				t.Errorf("%s %s priced %q, want %q", c.command, flag, lastWorkload, c.workload)
			}
		}
	}
}

func TestDeclaredCompletersCompletesFixedChoices(t *testing.T) {
	d := newDeclared(t)
	empty := completerContext(&fakeCloud{})

	cases := []struct {
		label    string
		complete completion.Completer
		want     []string
	}{
		{"instance create --compute-type", d.option("instance create", "--compute-type"), []string{"gpu", "cpu"}},
		{"volume find --type", d.option("volume find", "--type"), []string{"f", "d"}},
		{"pricing list --workload", d.option("pricing list", "--workload"), []string{"instance", "run-job"}},
		{"exec --preemption-retries", d.option("exec", "--preemption-retries"), []string{"0", "1", "2", "3"}},
		{"templates create --visibility", d.option("templates create", "--visibility"), []string{"private", "organization"}},
		{"completion", d.argument("completion"), []string{"bash", "zsh"}},
	}

	for _, c := range cases {
		got := values(t, c.complete, empty)
		wantValues(t, c.label, got, c.want)
	}
}
