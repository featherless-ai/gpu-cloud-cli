package job

import (
	"context"
	"slices"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"featherless.ai/featherless-cloud/internal/api"
	"featherless.ai/featherless-cloud/internal/commands/instance"
	"featherless.ai/featherless-cloud/internal/commands/shared"
	"featherless.ai/featherless-cloud/internal/completion"
	"featherless.ai/featherless-cloud/internal/errs"
	"featherless.ai/featherless-cloud/internal/output"
)

// TerminalStates are the states a Run Job never leaves.
var TerminalStates = []api.RunJobState{api.JobSucceeded, api.JobFailed, api.JobCancelled, api.JobTimedOut, api.JobInterrupted}

const (
	followInterval = 2 * time.Second
	// finishedGrace is how long to wait for late output after the job ends.
	finishedGrace = 10 * time.Second
)

// attempts completes the attempts with recorded output of the job named by
// the first argument.
func attempts(word string, ctx *completion.Context) ([]string, error) {
	if len(ctx.Args) == 0 || ctx.Args[0] == "" {
		return []string{}, nil
	}

	cloud, err := ctx.Cloud()
	if err != nil {
		return nil, err
	}

	job, err := cloud.GetJob(context.Background(), ctx.Args[0])
	if err != nil {
		return nil, err
	}

	var numbers []string

	for _, attempt := range job.Attempts {
		if attempt.ContainerRunID != nil && *attempt.ContainerRunID != "" {
			numbers = append(numbers, strconv.Itoa(attempt.Number))
		}
	}

	return shared.StartsWith(numbers, word), nil
}

type logsOptions struct {
	head    bool
	tail    string
	follow  bool
	attempt string
	profile string
}

func showAttempt(cmd *cobra.Command, ctx *output.Context, id string, opts *logsOptions) error {
	if opts.follow {
		return errs.New("--attempt cannot be combined with --follow")
	}

	number := shared.ParseNumber(opts.attempt)

	client, err := shared.Client(opts.profile)
	if err != nil {
		return err
	}

	job, err := output.Spin(ctx, "Fetching Run Job", func() (api.RunJob, error) {
		return client.GetJob(cmd.Context(), id)
	})

	if err != nil {
		return err
	}

	index := slices.IndexFunc(job.Attempts, func(attempt api.RunJobAttempt) bool {
		return float64(attempt.Number) == number
	})

	if index < 0 {
		return errs.Newf("Run Job %s has no attempt %s; it has %d.", job.ShortID, opts.attempt, len(job.Attempts))
	}

	runID := job.Attempts[index].ContainerRunID

	if runID == nil || *runID == "" {
		return errs.Newf("No output was recorded for attempt %s.", shared.FormatNumber(number))
	}

	history, err := output.Spin(ctx, "Fetching logs", func() (api.RunJobLogHistory, error) {
		return client.JobLogHistory(cmd.Context(), job.ID, *runID, instance.Section(opts.head))
	})

	if err != nil {
		return err
	}

	ctx.Output(history, func() output.Result { return output.Result{Text: history.Text} })

	return nil
}

func showLogs(cmd *cobra.Command, id string, opts *logsOptions) error {
	ctx := shared.NewContext(cmd)

	if opts.head && opts.follow {
		return errs.New("--head cannot be combined with --follow")
	}

	if cmd.Flags().Changed("attempt") {
		return showAttempt(cmd, ctx, id, opts)
	}

	tail, err := instance.ParseTail(opts.tail)
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

	section := instance.Section(opts.head)

	first, err := output.Spin(ctx, "Fetching logs", func() (api.RunJobLogs, error) {
		return client.JobLogs(cmd.Context(), id, tail, section)
	})

	if err != nil {
		return err
	}

	ctx.Output(first, func() output.Result { return output.Result{Text: first.Logs} })

	if !opts.follow || first.Complete {
		return nil
	}

	previous := first.Logs
	var finishedAt time.Time

	for {
		time.Sleep(followInterval)
		job, jobErr := client.GetJob(cmd.Context(), id)
		latest, logsErr := client.JobLogs(cmd.Context(), id, tail, section)

		if logsErr == nil {
			instance.PrintDelta(ctx, instance.NewContent(previous, latest.Logs))
			previous = instance.NextLogCursor(previous, latest.Logs, latest.Complete)

			if latest.Complete {
				ctx.Log("logs stopped")
				return nil
			}
		}

		hasFinished := jobErr == nil && logsErr == nil && slices.Contains(TerminalStates, job.State)

		if hasFinished {
			if finishedAt.IsZero() {
				finishedAt = ctx.Now()
			}

			if ctx.Now().Sub(finishedAt) >= finishedGrace {
				ctx.Log("Job finished. More output may arrive; run logs again to check.")
				return nil
			}
		}

		if jobErr != nil && logsErr != nil {
			return errs.New("Unable to reconnect to the Run Job. Try the command again.")
		}
	}
}

func newLogsCommand() *cobra.Command {
	var opts logsOptions

	cmd := &cobra.Command{
		Use:   "logs <id>",
		Short: "Print bounded debug output; use your own logging service for complete logs",
		Args:  shared.Arguments,
		RunE: func(cmd *cobra.Command, args []string) error {
			return showLogs(cmd, args[0], &opts)
		},
	}

	completion.Args(cmd, completion.Arg{Name: "id", Description: "Run Job short id", Complete: shared.Jobs})
	shared.Switch(cmd, &opts.head, "--head", "show the captured beginning instead of recent output")
	shared.Option(cmd, &opts.tail, "--tail <n>", "500", "lines of history (1-5000)")
	shared.Switch(cmd, &opts.follow, "--follow", "keep streaming until the Run Job ends")
	shared.Option(cmd, &opts.attempt, "--attempt <n>", "", "output of an earlier attempt of a job that was started again")
	completion.Flag(cmd, "attempt", attempts)
	shared.Option(cmd, &opts.profile, "--profile <name>", "", "profile to use")
	completion.Flag(cmd, "profile", shared.Profiles)

	return cmd
}
