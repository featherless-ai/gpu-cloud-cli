package instance

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/coder/websocket"

	"featherless.ai/featherless-cloud/internal/api"
	"featherless.ai/featherless-cloud/internal/errs"
)

type frame struct {
	Type    string `json:"type"`
	Message string `json:"message,omitempty"`
}

// fakeSocket delivers its frames, then closes with closeCode.
type fakeSocket struct {
	mu        sync.Mutex
	frames    []frame
	closeCode websocket.StatusCode
}

func (s *fakeSocket) Read(ctx context.Context) (websocket.MessageType, []byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.frames) == 0 {
		return 0, nil, websocket.CloseError{Code: s.closeCode}
	}

	next := s.frames[0]
	s.frames = s.frames[1:]
	data, err := json.Marshal(next)

	return websocket.MessageText, data, err
}

func (s *fakeSocket) Write(ctx context.Context, typ websocket.MessageType, data []byte) error {
	return nil
}

func (s *fakeSocket) Close(code websocket.StatusCode, reason string) error {
	return nil
}

// attachTerminal stands in for an interactive terminal nobody types into.
func attachTerminal(t *testing.T) {
	t.Helper()
	keys, typing := io.Pipe()
	t.Cleanup(func() { _ = typing.Close() })

	swap(t, &isTerminal, func() bool { return true })
	swap(t, &makeRaw, func() (func(), error) { return func() {}, nil })
	swap(t, &terminalSize, func() (int, int, error) { return 0, 0, io.EOF })
	swap(t, &terminalIn, io.Reader(keys))
	swap(t, &terminalOut, io.Writer(&bytes.Buffer{}))
}

func TestReattachesTheTmuxSessionItOpenedAndExplainsAVanishedOneInCLITerms(t *testing.T) {
	var targets []api.TerminalTarget

	grants := []api.TerminalSession{
		{Token: "t1", WSPath: "/ws", TmuxSession: &api.TmuxSession{ID: "tmux-1", Name: "default"}},
		{Token: "t2", WSPath: "/ws"},
	}

	cloud := &fakeCloud{
		getInstance: func(id string) (api.Instance, error) {
			return api.Instance{Name: "research", ShortID: "rcsrvch012", State: api.InstanceRunning}, nil
		},
		sshKeys: func() ([]api.SSHKey, error) {
			return []api.SSHKey{{}}, nil
		},
		createTerminalSession: func(id string, columns, rows int, target api.TerminalTarget) (api.TerminalSession, error) {
			targets = append(targets, target)
			grant := grants[0]
			grants = grants[1:]

			return grant, nil
		},
	}

	sockets := []*fakeSocket{
		{frames: []frame{{Type: "ready"}}, closeCode: 4006},
		{frames: []frame{{Type: "error", Message: "Tmux session could not be opened. Refresh the session list and try again."}}, closeCode: 4007},
	}

	swap(t, &dial, func(ctx context.Context, url string) (socket, error) {
		next := sockets[0]
		sockets = sockets[1:]

		return next, nil
	})

	recorded := isolate(t, cloud)
	attachTerminal(t)
	err := run("instance", "shell", "rcsrvch012")

	if code, ok := errs.ExitCode(err); !ok || code != 1 {
		t.Errorf("error = %v, want exit 1", err)
	}

	want := []api.TerminalTarget{{Tmux: "default"}, {TmuxSessionID: "tmux-1"}}

	if !reflect.DeepEqual(targets, want) {
		t.Errorf("terminal targets = %+v, want %+v", targets, want)
	}

	message := "Tmux session unavailable. It may have ended. Run the command again to open a new one."

	if !strings.Contains(recorded.stderr.String(), message) {
		t.Errorf("stderr = %q, want %q", recorded.stderr.String(), message)
	}
}
