package instance

import (
	"errors"
	"io/fs"
	"net/netip"
	"os"
	"os/exec"
	"os/signal"
	"regexp"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"featherless.ai/featherless-cloud/internal/api"
	"featherless.ai/featherless-cloud/internal/commands/shared"
	"featherless.ai/featherless-cloud/internal/completion"
	"featherless.ai/featherless-cloud/internal/errs"
	"featherless.ai/featherless-cloud/internal/output"
)

// process is a started ssh client.
type process interface {
	Signal(sig os.Signal) error
	// Wait blocks until the process ends and returns its exit code, or -1 and
	// the signal that ended it.
	Wait() (int, syscall.Signal)
}

// Seams tests replace.
var (
	// spawn starts cmd as built: no local shell, the terminal's own stdio.
	spawn         = startProcess
	notifySignals = signal.Notify
	stopSignals   = signal.Stop
)

var (
	sessionNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
	hostPattern        = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9.-]*[A-Za-z0-9])?$`)
	sshUserPattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]*$`)
)

type execProcess struct {
	cmd *exec.Cmd
}

func (p execProcess) Signal(sig os.Signal) error {
	return p.cmd.Process.Signal(sig)
}

func (p execProcess) Wait() (int, syscall.Signal) {
	_ = p.cmd.Wait()
	state := p.cmd.ProcessState

	if state == nil {
		return 1, 0
	}

	if status, isWaitStatus := state.Sys().(syscall.WaitStatus); isWaitStatus && status.Signaled() {
		return -1, status.Signal()
	}

	return state.ExitCode(), 0
}

func startProcess(cmd *exec.Cmd) (process, error) {
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	return execProcess{cmd: cmd}, nil
}

// validateSessionName allows only literal, portable tmux names: no target or
// pattern syntax.
func validateSessionName(name string) (string, error) {
	if !sessionNamePattern.MatchString(name) {
		return "", errs.New("Session name must be 1–64 letters, digits, underscores or hyphens, starting with a letter or digit.")
	}

	return name, nil
}

// quote makes value one word for the remote login shell, which OpenSSH hands
// a single command string.
func quote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

// tmuxCommand is the remote command; -s, not a target, so new-session -A
// matches the exact session name.
func tmuxCommand(name string) (string, error) {
	if _, err := validateSessionName(name); err != nil {
		return "", err
	}

	missing := "tmux is not installed in this instance. Use an image with tmux, or install it yourself via instance ssh, then retry."

	return `command -v tmux >/dev/null 2>&1 || { printf '%s\n' ` + quote(missing) + ` >&2; exit 127; }; exec tmux new-session -A -s ` + quote(name), nil
}

func isValidHost(host string) bool {
	if _, err := netip.ParseAddr(host); err == nil {
		return true
	}

	return len(host) >= 1 && len(host) <= 253 && hostPattern.MatchString(host)
}

// sshArgs builds ssh's argv from the structured connection data only; the
// display-only ssh.command is never run. Empty identity or session means none.
func sshArgs(instance api.Instance, identity, session string) ([]string, error) {
	isUsable := instance.SSH != nil && isValidHost(instance.SSH.Host) && sshUserPattern.MatchString(instance.ShortID)

	if !isUsable {
		return nil, errs.New("Instance SSH connection details are unavailable or invalid. Refresh the instance and retry.")
	}

	var args []string

	if identity != "" {
		args = append(args, "-i", identity)
	}

	if session != "" {
		args = append(args, "-t")
	}

	args = append(args, instance.ShortID+"@"+instance.SSH.Host)

	if session != "" {
		remote, err := tmuxCommand(session)
		if err != nil {
			return nil, err
		}

		args = append(args, remote)
	}

	return args, nil
}

// runSSH runs the native client, keeping its terminal handling, config, agent
// and host-key checks, and returns its exit status.
func runSSH(args []string) (int, error) {
	cmd := exec.Command("ssh", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	child, err := spawn(cmd)

	if err != nil {
		if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
			return 0, errs.New("OpenSSH client not found. Install ssh on this computer and retry.")
		}

		return 0, errs.Newf("Could not start ssh: %s", err.Error())
	}

	// Signals sent to the CLI itself must also reach its native SSH child.
	signals := make(chan os.Signal, 1)
	exited := make(chan struct{})
	release := shared.TakeSignals()
	defer release()
	notifySignals(signals, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	go func() {
		for {
			select {
			case sig := <-signals:
				_ = child.Signal(sig)
			case <-exited:
				return
			}
		}
	}()

	code, signaled := child.Wait()
	close(exited)
	stopSignals(signals)

	if code >= 0 {
		return code, nil
	}

	if signaled != 0 {
		return 128 + int(signaled), nil
	}

	return 1, nil
}

type sshOptions struct {
	identity string
	profile  string
}

// connect keeps the API's authentication apart from SSH's own.
func connect(cmd *cobra.Command, rawID string, opts sshOptions, session string) error {
	ctx := shared.NewContext(cmd)

	if ctx.Mode == output.JSON {
		return ctx.Fatal("instance ssh and instance tmux do not support --json.")
	}

	if session != "" {
		if _, err := validateSessionName(session); err != nil {
			return err
		}
	}

	client, err := shared.Client(opts.profile)
	if err != nil {
		return err
	}

	id, err := shared.ResolveInstanceID(cmd.Context(), client, rawID)
	if err != nil {
		return err
	}

	instance, err := client.GetInstance(cmd.Context(), id)
	if err != nil {
		return err
	}

	if instance.State != api.InstanceRunning {
		return ctx.Fatal("Instance " + instance.Name + " is " + string(instance.State) + ". Start it first: featherless-cloud instance start " + instance.ShortID)
	}

	if instance.SSH != nil && !instance.SSH.HasKeys {
		return ctx.Fatal("No SSH keys registered. Add one with featherless-cloud ssh-keys add, then retry.")
	}

	args, err := sshArgs(instance, opts.identity, session)
	if err != nil {
		return err
	}

	code, err := runSSH(args)
	if err != nil {
		return err
	}

	if code != 0 {
		return &errs.Exit{Code: code}
	}

	return nil
}

func newSSHCommand() *cobra.Command {
	var opts sshOptions

	cmd := &cobra.Command{
		Use:   "ssh <id>",
		Short: "Open a shell using your local OpenSSH client",
		Args:  shared.Arguments,
		RunE: func(cmd *cobra.Command, args []string) error {
			return connect(cmd, args[0], opts, "")
		},
	}

	completion.Args(cmd, completion.Arg{Name: "id", Description: "instance name or short id", Complete: shared.Instances})
	shared.Option(cmd, &opts.identity, "-i, --identity <file>", "", "SSH private key file (otherwise use SSH config/agent)")
	shared.Option(cmd, &opts.profile, "--profile <name>", "", "profile to use")
	completion.Flag(cmd, "profile", shared.Profiles)
	shared.HelpAfter(cmd, "\nUses native SSH config, agent and host-key checks. Register your public key in the console first.\nExit with exit, or Enter then ~. (the normal SSH escape).")

	return cmd
}

func newTmuxCommand() *cobra.Command {
	var opts sshOptions

	cmd := &cobra.Command{
		Use:   "tmux <id> [name]",
		Short: "Create or attach an exact named tmux session over SSH (default: default, the session the browser terminal opens)",
		Args:  shared.Arguments,
		RunE: func(cmd *cobra.Command, args []string) error {
			session := "default"

			if len(args) > 1 {
				session = args[1]
			}

			return connect(cmd, args[0], opts, session)
		},
	}

	completion.Args(cmd, completion.Arg{Name: "id", Description: "instance name or short id", Complete: shared.Instances}, completion.Arg{Name: "name", Description: "tmux session name"})
	shared.Option(cmd, &opts.identity, "-i, --identity <file>", "", "SSH private key file (otherwise use SSH config/agent)")
	shared.Option(cmd, &opts.profile, "--profile <name>", "", "profile to use")
	completion.Flag(cmd, "profile", shared.Profiles)
	shared.HelpAfter(cmd, "\nNames: 1–64 letters, digits, underscores or hyphens; start with a letter or digit.\nUses tmux on the default socket for the SSH login user; tmux must already be installed.\nDetach with Ctrl-b d; reconnect with the same name. Enter then ~. disconnects SSH.\nSessions survive disconnects, not instance stop/restart/replacement or tmux server exit.\nNo software is installed and no SSH keys are registered automatically.")

	return cmd
}
