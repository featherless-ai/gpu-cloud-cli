package instance

import (
	"context"
	"io"
	"os"
	"time"

	"github.com/coder/websocket"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"featherless.ai/featherless-cloud/internal/commands/shared"
	"featherless.ai/featherless-cloud/internal/completion"
	"featherless.ai/featherless-cloud/internal/errs"
)

// socket is the terminal WebSocket; *websocket.Conn implements it.
type socket interface {
	Read(ctx context.Context) (websocket.MessageType, []byte, error)
	Write(ctx context.Context, typ websocket.MessageType, data []byte) error
	Close(code websocket.StatusCode, reason string) error
}

// Seams tests replace.
var (
	dial = func(ctx context.Context, url string) (socket, error) {
		conn, _, err := websocket.Dial(ctx, url, nil)

		if err != nil {
			return nil, err
		}

		return conn, nil
	}
	isTerminal = func() bool {
		return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
	}
	// makeRaw puts the local terminal in raw mode and returns how to restore it.
	makeRaw = func() (func(), error) {
		fd := int(os.Stdin.Fd())
		state, err := term.MakeRaw(fd)

		if err != nil {
			return nil, err
		}

		return func() { _ = term.Restore(fd, state) }, nil
	}
	terminalSize = func() (columns, rows int, err error) {
		return term.GetSize(int(os.Stdout.Fd()))
	}
	terminalIn  io.Reader = os.Stdin
	terminalOut io.Writer = os.Stdout
	retryDelays           = []time.Duration{500 * time.Millisecond, time.Second, 2 * time.Second}
)

func newShellCommand() *cobra.Command {
	var opts struct {
		profile string
	}

	cmd := &cobra.Command{
		Use:   "shell <id>",
		Short: "Open an interactive shell in the instance (the shared tmux session \"default\")",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return errs.New("not implemented")
		},
	}

	completion.Args(cmd, completion.Arg{Name: "id", Complete: shared.Instances})
	shared.Option(cmd, &opts.profile, "--profile <name>", "", "profile to use")
	completion.Flag(cmd, "profile", shared.Profiles)

	return cmd
}
