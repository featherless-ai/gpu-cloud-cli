package instance

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"featherless.ai/featherless-cloud/internal/api"
	"featherless.ai/featherless-cloud/internal/commands/shared"
	"featherless.ai/featherless-cloud/internal/completion"
	"featherless.ai/featherless-cloud/internal/errs"
	"featherless.ai/featherless-cloud/internal/output"
)

const followInterval = 5 * time.Second

// NewContent is the part of next not already printed from the previous tail.
func NewContent(previous, next string) string {
	if previous == next {
		return ""
	}

	if strings.HasPrefix(next, previous) {
		return next[len(previous):]
	}

	// The server returns a bounded tail slice: old lines fell off, or the
	// container restarted. Find the longest overlap between next's head and
	// previous's tail so shared lines are never printed twice.
	var previousLines []string

	if previous != "" {
		previousLines = strings.Split(previous, "\n")
	}

	nextLines := strings.Split(next, "\n")

	for overlap := min(len(previousLines), len(nextLines)); overlap > 0; overlap-- {
		head := strings.Join(nextLines[:overlap], "\n")
		tail := strings.Join(previousLines[len(previousLines)-overlap:], "\n")

		if head == tail {
			return strings.Join(nextLines[overlap:], "\n")
		}
	}

	return next
}

// NextLogCursor keeps the overlap state while live logs hand off to the final
// retained logs.
func NextLogCursor(previous, next string, complete bool) string {
	if next != "" || complete || previous == "" {
		return next
	}

	return previous
}

// ParseTail reads --tail.
func ParseTail(value string) (int, error) {
	tail := shared.ParseNumber(value)

	if !shared.IsInteger(tail) || tail < 1 || tail > 5000 {
		return 0, errs.New("--tail must be between 1 and 5000")
	}

	return int(tail), nil
}

// Section is the part of the captured output --head picks.
func Section(head bool) api.LogSection {
	if head {
		return api.SectionHead
	}

	return api.SectionTail
}

// PrintDelta prints new followed output, ending it with a newline.
func PrintDelta(ctx *output.Context, delta string) {
	if delta == "" {
		return
	}

	if !strings.HasSuffix(delta, "\n") {
		delta += "\n"
	}

	fmt.Fprint(ctx.Stdout, delta)
}

func newLogsCommand() *cobra.Command {
	var opts struct {
		head    bool
		tail    string
		follow  bool
		profile string
	}

	cmd := &cobra.Command{
		Use:   "logs <id>",
		Short: "Print bounded debug output; use your own logging service for complete logs",
		Args:  shared.Arguments,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := shared.NewContext(cmd)

			if opts.head && opts.follow {
				return errs.New("--head cannot be combined with --follow")
			}

			tail, err := ParseTail(opts.tail)
			if err != nil {
				return err
			}

			if opts.follow && ctx.Mode == output.JSON {
				return ctx.Fatal("--follow cannot be combined with --json")
			}

			client, err := shared.Client(opts.profile)
			if err != nil {
				return err
			}

			id, err := shared.ResolveInstanceID(cmd.Context(), client, args[0])
			if err != nil {
				return err
			}

			section := Section(opts.head)

			first, err := client.InstanceLogs(cmd.Context(), id, tail, section)

			if err != nil {
				return err
			}

			ctx.Output(first, func() output.Result { return output.Result{Text: first.Logs} })

			if !opts.follow {
				return nil
			}

			previous := first.Logs

			for {
				time.Sleep(followInterval)
				instance, instanceErr := client.GetInstance(cmd.Context(), id)
				latest, logsErr := client.InstanceLogs(cmd.Context(), id, tail, section)

				if logsErr == nil {
					PrintDelta(ctx, NewContent(previous, latest.Logs))
					previous = latest.Logs
				}

				if instanceErr != nil || instance.State != api.InstanceRunning {
					ctx.Log("logs stopped")
					return nil
				}
			}
		},
	}

	completion.Args(cmd, completion.Arg{Name: "id", Description: "instance name or short id", Complete: shared.Instances})
	shared.Switch(cmd, &opts.head, "--head", "show the captured beginning instead of recent output")
	shared.Option(cmd, &opts.tail, "--tail <n>", "500", "lines of history (1-5000)")
	shared.Switch(cmd, &opts.follow, "--follow", "keep streaming new lines")
	shared.Option(cmd, &opts.profile, "--profile <name>", "", "profile to use")
	completion.Flag(cmd, "profile", shared.Profiles)

	return cmd
}
