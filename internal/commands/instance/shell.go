package instance

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/coder/websocket"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"featherless.ai/featherless-cloud/internal/api"
	"featherless.ai/featherless-cloud/internal/commands/shared"
	"featherless.ai/featherless-cloud/internal/completion"
	"featherless.ai/featherless-cloud/internal/errs"
	"featherless.ai/featherless-cloud/internal/output"
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

		conn.SetReadLimit(-1)

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

// resizeSignal is SIGWINCH on Linux and macOS; Windows never sends it.
const resizeSignal = syscall.Signal(0x1c)

// abnormalClosure is the close code of a socket that dropped without one.
const abnormalClosure websocket.StatusCode = 1006

// closeMessages mirror the console's terminal component.
var closeMessages = map[websocket.StatusCode]string{
	4001: "Terminal session expired.",
	4002: "You do not have access to this terminal.",
	4003: "Terminal unavailable. Make sure the instance is running.",
	4004: "Terminal could not start.",
	4005: "Terminal closed after being idle.",
	4006: "Terminal connection lost.",
	4007: "Tmux session unavailable. It may have ended. Run the command again to open a new one.",
}

const connectionLost websocket.StatusCode = 4006

type controlFrame struct {
	Type    string          `json:"type"`
	Status  json.RawMessage `json:"status"`
	Message any             `json:"message"`
}

// exitCodeOf reads the exit frame's status: a number, or an object that is
// "Success" or names the failed command's code in details.causes.
func exitCodeOf(raw json.RawMessage) int {
	var status any

	if err := json.Unmarshal(raw, &status); err != nil {
		return 0
	}

	switch typed := status.(type) {
	case float64:
		return int(typed)
	case []any:
		return 1
	case map[string]any:
		if typed["status"] == "Success" {
			return 0
		}

		details, _ := typed["details"].(map[string]any)
		causes, _ := details["causes"].([]any)

		for _, cause := range causes {
			fields, _ := cause.(map[string]any)

			if fields["reason"] != "ExitCode" {
				continue
			}

			text, _ := fields["message"].(string)
			code := shared.ParseNumber(text)

			if shared.IsInteger(code) {
				return int(code)
			}

			break
		}

		return 1
	}

	return 0
}

type sessionResult struct {
	// exitStatus is the shell's exit status, or nil when the socket closed without one.
	exitStatus   *int
	closeCode    websocket.StatusCode
	serverError  string
	reachedReady bool
}

func clamp(value, fallback int) int {
	if value < 1 {
		return fallback
	}

	return min(value, 1000)
}

// formEncode escapes like URLSearchParams.
func formEncode(value string) string {
	var out strings.Builder

	for _, b := range []byte(value) {
		isPlain := b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || strings.IndexByte("*-._", b) >= 0

		switch {
		case isPlain:
			out.WriteByte(b)
		case b == ' ':
			out.WriteByte('+')
		default:
			fmt.Fprintf(&out, "%%%02X", b)
		}
	}

	return out.String()
}

func terminalURL(apiBase string, session api.TerminalSession, columns, rows int) string {
	base := apiBase

	if strings.HasPrefix(base, "http") {
		base = "ws" + strings.TrimPrefix(base, "http")
	}

	query := "token=" + formEncode(session.Token) + "&cols=" + strconv.Itoa(columns) + "&rows=" + strconv.Itoa(rows)

	return base + session.WSPath + "?" + query
}

// pumpKeys forwards what is typed for the whole command; each session takes
// the keys while it is open.
func pumpKeys(input io.Reader, keys chan<- []byte) {
	buffer := make([]byte, 4096)

	for {
		n, err := input.Read(buffer)

		if n > 0 {
			keys <- append([]byte(nil), buffer[:n]...)
		}

		if err != nil {
			return
		}
	}
}

// runSession opens one terminal session and returns when the shell exits or
// the socket closes.
func runSession(ctx context.Context, url string, keys <-chan []byte) (sessionResult, error) {
	conn, err := dial(ctx, url)

	if err != nil {
		return sessionResult{closeCode: abnormalClosure}, nil
	}

	restore, err := makeRaw()
	if err != nil {
		_ = conn.Close(websocket.StatusNormalClosure, "")
		return sessionResult{}, err
	}

	defer restore()

	done := make(chan struct{})
	defer close(done)

	resized := make(chan os.Signal, 1)
	notifySignals(resized, resizeSignal)
	defer stopSignals(resized)

	go func() {
		for {
			select {
			case <-done:
				return
			case chunk := <-keys:
				_ = conn.Write(ctx, websocket.MessageBinary, chunk)
			case <-resized:
				columns, rows, err := terminalSize()

				if err == nil && columns > 0 && rows > 0 {
					frame := `{"type":"resize","columns":` + strconv.Itoa(columns) + `,"rows":` + strconv.Itoa(rows) + `}`
					_ = conn.Write(ctx, websocket.MessageText, []byte(frame))
				}
			}
		}
	}()

	var result sessionResult

	for {
		kind, data, err := conn.Read(ctx)

		if err != nil {
			result.closeCode = websocket.CloseStatus(err)

			if result.closeCode == -1 {
				result.closeCode = abnormalClosure
			}

			return result, nil
		}

		if kind == websocket.MessageBinary {
			_, _ = terminalOut.Write(data)
			continue
		}

		var frame controlFrame

		if err := json.Unmarshal(data, &frame); err != nil {
			continue
		}

		switch frame.Type {
		case "ready":
			result.reachedReady = true
		case "exit":
			status := exitCodeOf(frame.Status)
			result.exitStatus = &status
			result.closeCode = websocket.StatusNormalClosure
			_ = conn.Close(websocket.StatusNormalClosure, "")

			return result, nil
		case "error":
			message, _ := frame.Message.(string)

			if message == "" {
				message = "Terminal could not start."
			}

			result.serverError = message
		}
	}
}

func openShell(cmd *cobra.Command, rawID, profile string) error {
	ctx := shared.NewContext(cmd)

	if ctx.Mode == output.JSON || !isTerminal() {
		return ctx.Fatal("instance shell needs an interactive terminal.")
	}

	auth, err := shared.ResolveAuth(profile)
	if err != nil {
		return err
	}

	client := shared.NewCloud(auth.APIBase, auth.APIKey)

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

	keys, err := client.SSHKeys(cmd.Context())

	if err != nil || len(keys) == 0 {
		ctx.Warn("No SSH keys registered — web terminal only. Add one with featherless-cloud ssh-keys add.")
	}

	width, height, err := terminalSize()

	if err != nil {
		width, height = 0, 0
	}

	columns := clamp(width, 80)
	rows := clamp(height, 24)
	typed := make(chan []byte)
	retry := 0
	hadReadyChain := false
	tmuxSessionID := ""

	go pumpKeys(terminalIn, typed)

	for {
		// A fresh grant on reconnect reattaches the exact session it attached
		// to; a vanished tmux session is never recreated.
		target := api.TerminalTarget{Tmux: "default"}

		if tmuxSessionID != "" {
			target = api.TerminalTarget{TmuxSessionID: tmuxSessionID}
		}

		session, err := client.CreateTerminalSession(cmd.Context(), id, columns, rows, target)
		if err != nil {
			return err
		}

		if session.TmuxSession != nil {
			tmuxSessionID = session.TmuxSession.ID
		}

		// Grants are single-use with a 15 s lifetime: connect right after minting.
		url := terminalURL(auth.APIBase, session, columns, rows)

		result, err := runSession(cmd.Context(), url, typed)
		if err != nil {
			return err
		}

		if result.exitStatus != nil {
			if *result.exitStatus == 0 {
				return nil
			}

			return &errs.Exit{Code: *result.exitStatus}
		}

		if result.reachedReady {
			hadReadyChain = true
		}

		isRetryable := result.closeCode == connectionLost && hadReadyChain && retry < len(retryDelays)

		if isRetryable {
			ctx.Log(closeMessages[connectionLost])
			time.Sleep(retryDelays[retry])
			retry++

			continue
		}

		if result.closeCode == websocket.StatusNormalClosure {
			return nil
		}

		// The server's 4007 text points at the browser's session list.
		message := result.serverError

		if result.closeCode == 4007 {
			message = closeMessages[4007]
		}

		if message == "" {
			message = closeMessages[result.closeCode]
		}

		if message == "" {
			message = "Terminal closed."
		}

		ctx.Error(message)

		return &errs.Exit{Code: 1}
	}
}

func newShellCommand() *cobra.Command {
	var opts struct {
		profile string
	}

	cmd := &cobra.Command{
		Use:   "shell <id>",
		Short: "Open an interactive shell in the instance (the shared tmux session \"default\")",
		Args:  shared.Arguments,
		RunE: func(cmd *cobra.Command, args []string) error {
			return openShell(cmd, args[0], opts.profile)
		},
	}

	completion.Args(cmd, completion.Arg{Name: "id", Complete: shared.Instances})
	shared.Option(cmd, &opts.profile, "--profile <name>", "", "profile to use")
	completion.Flag(cmd, "profile", shared.Profiles)

	return cmd
}
