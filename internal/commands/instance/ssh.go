package instance

import (
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"featherless.ai/featherless-cloud/internal/api"
	"featherless.ai/featherless-cloud/internal/commands/shared"
	"featherless.ai/featherless-cloud/internal/completion"
	"featherless.ai/featherless-cloud/internal/errs"
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

func startProcess(cmd *exec.Cmd) (process, error) {
	return nil, errs.New("not implemented")
}

// validateSessionName allows only literal, portable tmux names: no target or
// pattern syntax.
func validateSessionName(name string) (string, error) {
	return "", errs.New("not implemented")
}

// tmuxCommand is the remote command; -s, not a target, so new-session -A
// matches the exact session name.
func tmuxCommand(name string) (string, error) {
	return "", errs.New("not implemented")
}

// sshArgs builds ssh's argv from the structured connection data only; the
// display-only ssh.command is never run. Empty identity or session means none.
func sshArgs(instance api.Instance, identity, session string) ([]string, error) {
	return nil, errs.New("not implemented")
}

// runSSH runs the native client, keeping its terminal handling, config, agent
// and host-key checks, and returns its exit status.
func runSSH(args []string) (int, error) {
	return 0, errs.New("not implemented")
}

type sshOptions struct {
	identity string
	profile  string
}

func newSSHCommand() *cobra.Command {
	var opts sshOptions

	cmd := &cobra.Command{
		Use:   "ssh <id>",
		Short: "Open a shell using your local OpenSSH client",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return errs.New("not implemented")
		},
	}

	completion.Args(cmd, completion.Arg{Name: "id", Complete: shared.Instances})
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
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return errs.New("not implemented")
		},
	}

	completion.Args(cmd, completion.Arg{Name: "id", Complete: shared.Instances}, completion.Arg{Name: "name"})
	shared.Option(cmd, &opts.identity, "-i, --identity <file>", "", "SSH private key file (otherwise use SSH config/agent)")
	shared.Option(cmd, &opts.profile, "--profile <name>", "", "profile to use")
	completion.Flag(cmd, "profile", shared.Profiles)
	shared.HelpAfter(cmd, "\nNames: 1–64 letters, digits, underscores or hyphens; start with a letter or digit.\nUses tmux on the default socket for the SSH login user; tmux must already be installed.\nDetach with Ctrl-b d; reconnect with the same name. Enter then ~. disconnects SSH.\nSessions survive disconnects, not instance stop/restart/replacement or tmux server exit.\nNo software is installed and no SSH keys are registered automatically.")

	return cmd
}
