package instance

import (
	"bytes"
	"context"
	"io"
	"reflect"
	"testing"

	"github.com/spf13/cobra"

	"featherless.ai/featherless-cloud/internal/api"
	"featherless.ai/featherless-cloud/internal/commands/shared"
	"featherless.ai/featherless-cloud/internal/completion"
	"featherless.ai/featherless-cloud/internal/config"
	"featherless.ai/featherless-cloud/internal/output"
)

func swap[T any](t *testing.T, target *T, value T) {
	t.Helper()
	previous := *target
	*target = value
	t.Cleanup(func() { *target = previous })
}

// fakeCloud answers only the calls a test sets; any other call panics.
type fakeCloud struct {
	api.Cloud
	getInstance           func(id string) (api.Instance, error)
	sshKeys               func() ([]api.SSHKey, error)
	createTerminalSession func(id string, columns, rows int, target api.TerminalTarget) (api.TerminalSession, error)
}

func (f *fakeCloud) GetInstance(ctx context.Context, id string) (api.Instance, error) {
	return f.getInstance(id)
}

func (f *fakeCloud) SSHKeys(ctx context.Context) ([]api.SSHKey, error) {
	return f.sshKeys()
}

func (f *fakeCloud) CreateTerminalSession(ctx context.Context, id string, columns, rows int, target api.TerminalTarget) (api.TerminalSession, error) {
	return f.createTerminalSession(id, columns, rows, target)
}

// session records what a command run asked for and printed.
type session struct {
	profiles []string
	stdout   bytes.Buffer
	stderr   bytes.Buffer
}

// isolate signs in as profile work against cloud and captures the output.
func isolate(t *testing.T, cloud api.Cloud) *session {
	t.Helper()
	recorded := &session{}

	swap(t, &shared.ResolveAuth, func(profile string) (config.ResolvedAuth, error) {
		recorded.profiles = append(recorded.profiles, profile)
		return config.ResolvedAuth{APIBase: "https://api.example", APIKey: "key", Profile: "work"}, nil
	})

	swap(t, &shared.NewCloud, func(apiBase, apiKey string) api.Cloud { return cloud })

	swap(t, &shared.NewContext, func(cmd *cobra.Command) *output.Context {
		ctx := output.FromCommand(cmd)
		ctx.Stdout = &recorded.stdout
		ctx.Stderr = &recorded.stderr

		return ctx
	})

	return recorded
}

// program is the CLI root with only --json and the instance group.
func program() *cobra.Command {
	root := &cobra.Command{Use: "featherless-cloud", SilenceErrors: true, SilenceUsage: true}
	root.PersistentFlags().Bool("json", false, "")
	root.SetOut(io.Discard)
	root.SetErr(io.Discard)
	root.AddCommand(Command())

	return root
}

func run(args ...string) error {
	root := program()
	root.SetArgs(args)

	return root.Execute()
}

// sameCompleter reports whether got is the completer function want.
func sameCompleter(got, want completion.Completer) bool {
	if got == nil || want == nil {
		return got == nil && want == nil
	}

	return reflect.ValueOf(got).Pointer() == reflect.ValueOf(want).Pointer()
}
