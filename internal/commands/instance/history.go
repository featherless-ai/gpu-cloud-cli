package instance

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"featherless.ai/featherless-cloud/internal/api"
	"featherless.ai/featherless-cloud/internal/commands/shared"
	"featherless.ai/featherless-cloud/internal/completion"
	"featherless.ai/featherless-cloud/internal/output"
)

var whitespace = regexp.MustCompile(`\s+`)

// truncate puts the message on one line of at most 80 characters.
func truncate(message string) string {
	single := strings.TrimSpace(whitespace.ReplaceAllString(message, " "))

	if shared.UTF16Length(single) > 80 {
		return shared.Slice(single, 79) + "…"
	}

	return single
}

func newHistoryCommand() *cobra.Command {
	var opts struct {
		profile string
	}

	cmd := &cobra.Command{
		Use:   "history <id>",
		Short: "Show instance lifecycle events",
		Args:  shared.Arguments,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := shared.NewContext(cmd)

			client, err := shared.Client(opts.profile)
			if err != nil {
				return err
			}

			id, err := shared.ResolveInstanceID(cmd.Context(), client, args[0])
			if err != nil {
				return err
			}

			events, err := output.Spin(ctx, "Fetching history", func() ([]api.InstanceEvent, error) {
				return client.InstanceHistory(cmd.Context(), id)
			})

			if err != nil {
				return err
			}

			ctx.Output(events, func() output.Result {
				sorted := slices.Clone(events)

				slices.SortStableFunc(sorted, func(a, b api.InstanceEvent) int {
					return strings.Compare(a.LastObservedAt, b.LastObservedAt)
				})

				rows := make([][]string, len(sorted))

				for i, event := range sorted {
					lastSeen := shared.Slice(strings.Replace(event.LastObservedAt, "T", " ", 1), 19)
					rows[i] = []string{string(event.Type), event.Reason, strconv.Itoa(event.Count), lastSeen, truncate(event.Message)}
				}

				table := &output.Table{Header: []string{"TYPE", "REASON", "COUNT", "LAST SEEN", "MESSAGE"}, Rows: rows}

				return output.Result{Table: table, Footer: fmt.Sprintf("%d event(s)", len(events))}
			})

			return nil
		},
	}

	completion.Args(cmd, completion.Arg{Name: "id", Description: "instance name or short id", Complete: shared.Instances})
	shared.Option(cmd, &opts.profile, "--profile <name>", "", "profile to use")
	completion.Flag(cmd, "profile", shared.Profiles)

	return cmd
}
