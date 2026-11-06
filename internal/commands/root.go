// Package commands builds the featherless-cloud command tree.
package commands

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"featherless.ai/featherless-cloud/internal/commands/billing"
	"featherless.ai/featherless-cloud/internal/commands/instance"
	"featherless.ai/featherless-cloud/internal/commands/job"
	"featherless.ai/featherless-cloud/internal/commands/pricing"
	"featherless.ai/featherless-cloud/internal/commands/registrykeys"
	"featherless.ai/featherless-cloud/internal/commands/shared"
	"featherless.ai/featherless-cloud/internal/commands/sshkeys"
	"featherless.ai/featherless-cloud/internal/commands/templates"
	"featherless.ai/featherless-cloud/internal/commands/volume"
	"featherless.ai/featherless-cloud/internal/errs"
	"featherless.ai/featherless-cloud/internal/update"
	"featherless.ai/featherless-cloud/internal/version"
)

// NewRoot builds the whole command tree, fresh on every call.
func NewRoot() *cobra.Command {
	cobra.EnableCommandSorting = false

	root := &cobra.Command{
		Use:               version.AppName,
		Short:             "Featherless GPU Cloud CLI",
		SilenceErrors:     true,
		SilenceUsage:      true,
		CompletionOptions: cobra.CompletionOptions{DisableDefaultCmd: true},
		// --version is a program option, so it also works after a subcommand.
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if !isVersionRequested(cmd) {
				return nil
			}

			fmt.Fprintln(cmd.OutOrStdout(), version.Version)
			return &errs.Exit{Code: 0}
		},
	}

	flags := root.PersistentFlags()
	flags.SortFlags = false
	flags.Bool("json", false, "output as JSON (for scripts and agents)")
	flags.Bool("non-interactive", false, "disable spinners and prompts")
	flags.BoolP("version", "V", false, "output the version number")
	root.SetFlagErrorFunc(describeFlagError)

	root.AddCommand(
		newLoginCommand(),
		newLogoutCommand(),
		instance.Command(),
		volume.Command(),
		pricing.Command(),
		billing.Command(),
		job.Command(),
		newExecCommand(),
		sshkeys.Command(),
		registrykeys.Command(),
		templates.Command(),
		newUpgradeCommand(),
		newCompletionCommand(),
		newCompleteCommand(),
		newCheckUpdateCommand(),
	)

	parseLikeCommander(root)
	root.SetHelpFunc(func(cmd *cobra.Command, _ []string) {
		printHelp(cmd, cmd.OutOrStdout())
	})
	root.SetHelpCommand(newHelpCommand(root))

	return root
}

func isVersionRequested(cmd *cobra.Command) bool {
	isRequested, err := cmd.Flags().GetBool("version")
	return err == nil && isRequested
}

// parseLikeCommander makes every command check its line as the TypeScript
// CLI's commander did: --version wins over argument errors, and a command
// group without a subcommand shows its help as an error.
func parseLikeCommander(cmd *cobra.Command) {
	isGroup := cmd.HasSubCommands() && cmd.RunE == nil && cmd.Run == nil

	if isGroup {
		cmd.Args = unknownCommand
		cmd.RunE = helpAsError
	}

	check := cmd.Args

	cmd.Args = func(cmd *cobra.Command, args []string) error {
		if isVersionRequested(cmd) || check == nil {
			return nil
		}

		return check(cmd, args)
	}

	for _, child := range cmd.Commands() {
		parseLikeCommander(child)
	}
}

func unknownCommand(cmd *cobra.Command, args []string) error {
	if len(args) == 0 || args[0] == "help" {
		return nil
	}

	candidates := []string{"help"}

	for _, child := range cmd.Commands() {
		if child.Hidden {
			continue
		}

		candidates = append(candidates, child.Name())

		if len(child.Aliases) > 0 {
			candidates = append(candidates, child.Aliases[0])
		}
	}

	return errs.Usagef("unknown command '%s'%s", args[0], suggestSimilar(args[0], candidates))
}

// helpAsError shows a group's help when no subcommand was given, as an
// error; `group help [command]` shows help normally, unless the command is
// unknown.
func helpAsError(cmd *cobra.Command, args []string) error {
	if len(args) > 0 {
		if len(args) == 1 {
			return cmd.Help()
		}

		found, rest, err := cmd.Find(args[1:2])
		isKnown := err == nil && len(rest) == 0 && found != cmd

		if isKnown {
			return found.Help()
		}
	}

	cmd.SetOut(cmd.ErrOrStderr())

	if err := cmd.Help(); err != nil {
		return err
	}

	return &errs.Exit{Code: 1}
}

// describeFlagError words a flag parsing failure as commander did.
func describeFlagError(cmd *cobra.Command, err error) error {
	var unknown *pflag.NotExistError
	var missing *pflag.ValueRequiredError
	var invalid *pflag.InvalidValueError

	switch {
	case errors.As(err, &unknown):
		if shorthands := unknown.GetSpecifiedShortnames(); shorthands != "" {
			return errs.Usagef("unknown option '-%s'", shorthands)
		}

		flag := "--" + unknown.GetSpecifiedName()

		return errs.Usagef("unknown option '%s'%s", flag, suggestSimilar(flag, longOptions(cmd)))
	case errors.As(err, &missing):
		return errs.Usagef("option '%s' argument missing", shared.Spec(missing.GetFlag()))
	case errors.As(err, &invalid):
		cause := errors.Unwrap(invalid)

		return errs.Usagef("option '%s' argument '%s' is invalid. %s", shared.Spec(invalid.GetFlag()), invalid.GetValue(), cause)
	}

	return errs.Usage(err.Error())
}

// longOptions are the visible long options of cmd and the commands above it.
func longOptions(cmd *cobra.Command) []string {
	var names []string

	add := func(flag *pflag.Flag) {
		if !flag.Hidden {
			names = append(names, "--"+flag.Name)
		}
	}

	cmd.Flags().VisitAll(add)
	cmd.InheritedFlags().VisitAll(add)

	if cmd.Flags().Lookup("help") == nil {
		names = append(names, "--help")
	}

	return names
}

// suggestSimilar is commander's "(Did you mean …?)" hint: the candidates
// closest to word, within three edits.
func suggestSimilar(word string, candidates []string) string {
	const maxDistance = 3
	isOption := strings.HasPrefix(word, "--")

	if isOption {
		word = strings.TrimPrefix(word, "--")
	}

	var similar []string
	bestDistance := maxDistance
	seen := map[string]bool{}

	for _, candidate := range candidates {
		if isOption {
			candidate = strings.TrimPrefix(candidate, "--")
		}

		if seen[candidate] || len([]rune(candidate)) <= 1 {
			continue
		}

		seen[candidate] = true
		distance := editDistance(word, candidate)
		length := max(len([]rune(word)), len([]rune(candidate)))
		similarity := float64(length-distance) / float64(length)

		if similarity <= 0.4 {
			continue
		}

		if distance < bestDistance {
			bestDistance = distance
			similar = []string{candidate}
		} else if distance == bestDistance {
			similar = append(similar, candidate)
		}
	}

	slices.Sort(similar)

	if isOption {
		for i, candidate := range similar {
			similar[i] = "--" + candidate
		}
	}

	switch len(similar) {
	case 0:
		return ""
	case 1:
		return "\n(Did you mean " + similar[0] + "?)"
	}

	return "\n(Did you mean one of " + strings.Join(similar, ", ") + "?)"
}

// editDistance is the optimal string alignment distance commander uses.
func editDistance(a, b string) int {
	first := []rune(a)
	second := []rune(b)

	if diff := len(first) - len(second); diff > 3 || diff < -3 {
		return max(len(first), len(second))
	}

	d := make([][]int, len(first)+1)

	for i := range d {
		d[i] = make([]int, len(second)+1)
		d[i][0] = i
	}

	for j := range second {
		d[0][j+1] = j + 1
	}

	for j := 1; j <= len(second); j++ {
		for i := 1; i <= len(first); i++ {
			cost := 1

			if first[i-1] == second[j-1] {
				cost = 0
			}

			d[i][j] = min(d[i-1][j]+1, d[i][j-1]+1, d[i-1][j-1]+cost)

			isTransposition := i > 1 && j > 1 && first[i-1] == second[j-2] && first[i-2] == second[j-1]

			if isTransposition {
				d[i][j] = min(d[i][j], d[i-2][j-2]+1)
			}
		}
	}

	return d[len(first)][len(second)]
}

func newCheckUpdateCommand() *cobra.Command {
	return &cobra.Command{
		Use:    "_check-update",
		Hidden: true,
		Args:   shared.Arguments,
		RunE: func(cmd *cobra.Command, args []string) error {
			update.RunUpdateCheck(cmd.Context())
			return nil
		},
	}
}
