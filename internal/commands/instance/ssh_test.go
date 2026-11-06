package instance

import (
	"bytes"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"featherless.ai/featherless-cloud/internal/api"
	"featherless.ai/featherless-cloud/internal/commands/shared"
	"featherless.ai/featherless-cloud/internal/completion"
	"featherless.ai/featherless-cloud/internal/errs"
)

func sshInstance() api.Instance {
	return api.Instance{
		ShortID: "rcsrvch012",
		Name:    "research",
		State:   api.InstanceRunning,
		SSH: &api.InstanceSSH{
			Host:    "ssh.example.com",
			HasKeys: true,
			Command: new("DO NOT EXECUTE; touch /tmp/bad"),
			Status:  "ready",
		},
	}
}

func withHost(host string) api.Instance {
	instance := sshInstance()
	instance.SSH.Host = host

	return instance
}

// fakeProcess is an ssh child that ends when done is closed.
type fakeProcess struct {
	signals chan os.Signal
	done    chan struct{}
	code    int
	signal  syscall.Signal
}

func (p *fakeProcess) Signal(sig os.Signal) error {
	p.signals <- sig
	return nil
}

func (p *fakeProcess) Wait() (int, syscall.Signal) {
	<-p.done
	return p.code, p.signal
}

func running() *fakeProcess {
	return &fakeProcess{signals: make(chan os.Signal, 1), done: make(chan struct{}), code: 0}
}

func exited(code int) *fakeProcess {
	child := running()
	child.code = code
	close(child.done)

	return child
}

func killedBy(signal syscall.Signal) *fakeProcess {
	child := exited(-1)
	child.signal = signal

	return child
}

// spawned records each command spawn starts, starting child or failing with err.
func spawned(t *testing.T, child process, err error) *[]*exec.Cmd {
	t.Helper()
	var calls []*exec.Cmd

	swap(t, &spawn, func(cmd *exec.Cmd) (process, error) {
		calls = append(calls, cmd)
		return child, err
	})

	return &calls
}

// signalHandler records the forwarding handler runSSH installs and removes.
type signalHandler struct {
	installed chan struct{}
	channel   chan<- os.Signal
	watched   []os.Signal
	removed   chan<- os.Signal
}

func handleSignals(t *testing.T) *signalHandler {
	t.Helper()
	handler := &signalHandler{installed: make(chan struct{})}

	swap(t, &notifySignals, func(channel chan<- os.Signal, watched ...os.Signal) {
		handler.channel = channel
		handler.watched = watched
		close(handler.installed)
	})

	swap(t, &stopSignals, func(channel chan<- os.Signal) {
		handler.removed = channel
	})

	return handler
}

func wantSSHArgs(t *testing.T, instance api.Instance, identity, session string, want []string) {
	t.Helper()
	got, err := sshArgs(instance, identity, session)

	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("sshArgs(%q, %q) = %q, %v; want %q", identity, session, got, err, want)
	}
}

func TestNativeSSHArgvUsesOnlyStructuredConnectionDataAndKeepsIdentityPathsAsOneArgument(t *testing.T) {
	remote, err := tmuxCommand("main")

	if err != nil {
		t.Fatal(err)
	}

	wantSSHArgs(t, sshInstance(), "", "", []string{"rcsrvch012@ssh.example.com"})
	wantSSHArgs(t, sshInstance(), "/key dir/$(bad)", "", []string{"-i", "/key dir/$(bad)", "rcsrvch012@ssh.example.com"})
	wantSSHArgs(t, sshInstance(), "", "main", []string{"-t", "rcsrvch012@ssh.example.com", remote})
}

func TestNativeSSHArgvQuotesTheRemoteNameAndProvidesAnActionableMissingTmuxFailure(t *testing.T) {
	named, err := tmuxCommand("train-1")

	if err != nil || !strings.Contains(named, "exec tmux new-session -A -s 'train-1'") {
		t.Errorf("tmuxCommand(train-1) = %q, %v", named, err)
	}

	remote, err := tmuxCommand("main")

	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"command -v tmux", "Use an image with tmux", "exit 127"} {
		if !strings.Contains(remote, want) {
			t.Errorf("tmuxCommand(main) = %q, want it to contain %q", remote, want)
		}
	}
}

func TestNativeSSHArgvRejectsUnsafeOrAmbiguousName(t *testing.T) {
	names := []string{"", "-bad", "_run", "a.b", "a:b", "=main", "foo*", "a b", "a'b", "a;exit", "a\n", strings.Repeat("a", 65)}

	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			_, err := validateSessionName(name)

			if err == nil || !strings.Contains(err.Error(), "Session name") {
				t.Errorf("validateSessionName(%q) error = %v", name, err)
			}
		})
	}
}

func TestNativeSSHArgvAcceptsLiteralName(t *testing.T) {
	for _, name := range []string{"main", "train-1", "run_1", "1", strings.Repeat("a", 64)} {
		t.Run(name, func(t *testing.T) {
			got, err := validateSessionName(name)

			if err != nil || got != name {
				t.Errorf("validateSessionName(%q) = %q, %v", name, got, err)
			}
		})
	}
}

func TestNativeSSHArgvRejectsUnsafeHost(t *testing.T) {
	for _, host := range []string{"-oProxyCommand=bad", "host;bad", "host\n", "user@host", "host/path", ""} {
		t.Run(host, func(t *testing.T) {
			_, err := sshArgs(withHost(host), "", "")

			if err == nil || !strings.Contains(err.Error(), "connection details") {
				t.Errorf("sshArgs(host %q) error = %v", host, err)
			}
		})
	}
}

func TestNativeSSHArgvAcceptsIPv6AndRejectsMalformedSSHUsersOrAbsentDetails(t *testing.T) {
	wantSSHArgs(t, withHost("::1"), "", "", []string{"rcsrvch012@::1"})

	malformedUser := sshInstance()
	malformedUser.ShortID = "-x@bad"
	_, err := sshArgs(malformedUser, "", "")

	if err == nil || !strings.Contains(err.Error(), "connection details") {
		t.Errorf("malformed user: error = %v", err)
	}

	noDetails := sshInstance()
	noDetails.SSH = nil
	_, err = sshArgs(noDetails, "", "")

	if err == nil || !strings.Contains(err.Error(), "connection details") {
		t.Errorf("no details: error = %v", err)
	}
}

func TestSSHProcessLifecycleInheritsStdioWithoutALocalShellAndPropagatesExit(t *testing.T) {
	for _, code := range []int{0, 1, 127, 255} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			handleSignals(t)
			calls := spawned(t, exited(code), nil)
			got, err := runSSH([]string{"user@host"})

			if len(*calls) != 1 {
				t.Fatalf("spawned %d processes, want 1", len(*calls))
			}

			cmd := (*calls)[0]

			if filepath.Base(cmd.Path) != "ssh" || !reflect.DeepEqual(cmd.Args, []string{"ssh", "user@host"}) {
				t.Errorf("spawned %q %q, want ssh user@host without a shell", cmd.Path, cmd.Args)
			}

			if cmd.Stdin != os.Stdin || cmd.Stdout != os.Stdout || cmd.Stderr != os.Stderr {
				t.Errorf("ssh does not inherit the terminal's stdio")
			}

			if err != nil || got != code {
				t.Errorf("runSSH = %d, %v; want %d", got, err, code)
			}
		})
	}
}

func TestSSHProcessLifecyclePropagatesSignal(t *testing.T) {
	cases := []struct {
		signal syscall.Signal
		code   int
	}{
		{syscall.SIGTERM, 143},
		{syscall.SIGINT, 130},
		{syscall.SIGKILL, 137},
	}

	for _, c := range cases {
		t.Run(c.signal.String(), func(t *testing.T) {
			handleSignals(t)
			spawned(t, killedBy(c.signal), nil)
			got, err := runSSH(nil)

			if err != nil || got != c.code {
				t.Errorf("runSSH = %d, %v; want %d", got, err, c.code)
			}
		})
	}
}

func TestSSHProcessLifecycleForwardsAndRemovesTheSignalHandlerOnExit(t *testing.T) {
	for _, signal := range []syscall.Signal{syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP} {
		t.Run(signal.String(), func(t *testing.T) {
			handler := handleSignals(t)
			child := running()
			spawned(t, child, nil)
			finished := make(chan struct{})

			go func() {
				_, _ = runSSH(nil)
				close(finished)
			}()

			select {
			case <-handler.installed:
			case <-time.After(time.Second):
				t.Fatal("no signal handler installed")
			}

			if !slices.Contains(handler.watched, os.Signal(signal)) {
				t.Errorf("handler watches %v, want %v", handler.watched, signal)
			}

			handler.channel <- signal

			select {
			case forwarded := <-child.signals:
				if forwarded != signal {
					t.Errorf("forwarded %v, want %v", forwarded, signal)
				}
			case <-time.After(time.Second):
				t.Errorf("%v was not forwarded to ssh", signal)
			}

			close(child.done)

			select {
			case <-finished:
			case <-time.After(time.Second):
				t.Fatal("runSSH did not return after ssh exited")
			}

			if handler.removed == nil || handler.removed != handler.channel {
				t.Errorf("signal handler not removed")
			}
		})
	}
}

func TestSSHProcessLifecycleReportsSpawnError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"ENOENT", &exec.Error{Name: "ssh", Err: exec.ErrNotFound}, "Install ssh"},
		{"EACCES", &fs.PathError{Op: "fork/exec", Path: "/usr/bin/ssh", Err: syscall.EACCES}, "Could not start ssh"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			handleSignals(t)
			spawned(t, nil, c.err)
			_, err := runSSH(nil)

			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("error = %v, want it to contain %q", err, c.want)
			}
		})
	}
}

func TestSSHProcessLifecycleResolvesInstanceProfileAndRuns(t *testing.T) {
	cases := []struct {
		command string
		session string
	}{
		{"ssh", ""},
		{"tmux", "default"},
		{"tmux", "training"},
	}

	for _, c := range cases {
		t.Run(c.command+" "+c.session, func(t *testing.T) {
			var requested []string
			instance := sshInstance()

			cloud := &fakeCloud{
				getInstance: func(id string) (api.Instance, error) {
					requested = append(requested, id)
					return instance, nil
				},
			}

			recorded := isolate(t, cloud)
			handleSignals(t)
			calls := spawned(t, exited(23), nil)
			args := []string{"instance", c.command, instance.ShortID, "-i", "/key dir/id", "--profile", "work"}

			if c.session == "training" {
				args = append(args, c.session)
			}

			err := run(args...)

			if !reflect.DeepEqual(recorded.profiles, []string{"work"}) {
				t.Errorf("authenticated as %q, want work", recorded.profiles)
			}

			if !reflect.DeepEqual(requested, []string{instance.ShortID}) {
				t.Errorf("fetched %q, want %s", requested, instance.ShortID)
			}

			want, _ := sshArgs(instance, "/key dir/id", c.session)

			if len(*calls) != 1 || !reflect.DeepEqual((*calls)[0].Args, append([]string{"ssh"}, want...)) {
				t.Errorf("spawned %v, want ssh %q", *calls, want)
			}

			if code, ok := errs.ExitCode(err); !ok || code != 23 {
				t.Errorf("error = %v, want exit 23", err)
			}
		})
	}
}

func TestSSHProcessLifecycleRefusesUnavailableSSHWithoutSpawningAProcess(t *testing.T) {
	stopped := sshInstance()
	stopped.State = api.InstanceStopped
	noKeys := sshInstance()
	noKeys.SSH.HasKeys = false
	noDetails := sshInstance()
	noDetails.SSH = nil

	cases := map[string]api.Instance{"stopped": stopped, "no keys": noKeys, "no details": noDetails}

	for name, unavailable := range cases {
		t.Run(name, func(t *testing.T) {
			cloud := &fakeCloud{
				getInstance: func(id string) (api.Instance, error) { return unavailable, nil },
			}

			isolate(t, cloud)
			handleSignals(t)
			calls := spawned(t, exited(0), nil)

			if err := run("instance", "ssh", sshInstance().ShortID); err == nil {
				t.Errorf("no error")
			}

			if len(*calls) != 0 {
				t.Errorf("spawned %d processes", len(*calls))
			}
		})
	}
}

func TestSSHProcessLifecycleRejectsJSONModeBeforeAuthenticationOrSpawning(t *testing.T) {
	recorded := isolate(t, &fakeCloud{})
	calls := spawned(t, exited(0), nil)
	err := run("--json", "instance", "ssh", "id")

	if err == nil || !strings.Contains(recorded.stdout.String(), "do not support --json") {
		t.Errorf("error = %v, stdout = %q; want the --json refusal", err, recorded.stdout.String())
	}

	if len(recorded.profiles) != 0 || len(*calls) != 0 {
		t.Errorf("authenticated %d times and spawned %d processes", len(recorded.profiles), len(*calls))
	}
}

func TestSSHProcessLifecycleValidatesNamesBeforeMakingAPICalls(t *testing.T) {
	recorded := isolate(t, &fakeCloud{})
	calls := spawned(t, exited(0), nil)
	err := run("instance", "tmux", "id", "bad:name")

	if err == nil || !strings.Contains(err.Error(), "Session name") {
		t.Errorf("error = %v, want the session name rule", err)
	}

	if len(recorded.profiles) != 0 || len(*calls) != 0 {
		t.Errorf("authenticated %d times and spawned %d processes", len(recorded.profiles), len(*calls))
	}
}

func findCommand(t *testing.T, root *cobra.Command, path ...string) *cobra.Command {
	t.Helper()
	cmd, rest, err := root.Find(path)

	if err != nil || len(rest) != 0 || cmd.Name() != path[len(path)-1] {
		t.Fatalf("no command %q", path)
	}

	return cmd
}

func TestRegistersShellSSHAndTmuxWithHelpAndCompletionOptions(t *testing.T) {
	root := program()
	group := findCommand(t, root, "instance")
	var names []string

	for _, cmd := range group.Commands() {
		names = append(names, cmd.Name())
	}

	for _, name := range []string{"shell", "ssh", "tmux"} {
		if !slices.Contains(names, name) {
			t.Errorf("instance has no %s command", name)
		}
	}

	tmux := findCommand(t, root, "instance", "tmux")
	var help bytes.Buffer
	tmux.SetOut(&help)

	if err := tmux.Help(); err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{"default: default", "-i, --identity <file>", "default socket", "Ctrl-b d", "stop/restart/replacement"} {
		if !strings.Contains(help.String(), want) {
			t.Errorf("tmux help does not contain %q:\n%s", want, help.String())
		}
	}

	for _, name := range []string{"ssh", "tmux"} {
		cmd := findCommand(t, root, "instance", name)
		identity := cmd.Flags().Lookup("identity")
		profile := cmd.Flags().Lookup("profile")

		if identity == nil || profile == nil {
			t.Fatalf("%s lacks --identity or --profile", name)
		}

		identityValue, _ := pflag.UnquoteUsage(identity)

		if identity.Shorthand != "i" || identityValue != "<file>" || identity.NoOptDefVal != "" {
			t.Errorf("%s: --identity is -%s, takes %q, want -i, --identity <file>", name, identity.Shorthand, identityValue)
		}

		profileValue, _ := pflag.UnquoteUsage(profile)

		if profileValue != "<name>" || profile.NoOptDefVal != "" {
			t.Errorf("%s: --profile takes %q, want --profile <name>", name, profileValue)
		}

		if !sameCompleter(completion.FlagCompleter(profile), shared.Profiles) {
			t.Errorf("%s: --profile does not complete profiles", name)
		}
	}
}
