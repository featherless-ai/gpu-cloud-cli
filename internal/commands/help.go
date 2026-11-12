package commands

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"golang.org/x/term"

	"featherless.ai/featherless-cloud/internal/commands/shared"
	"featherless.ai/featherless-cloud/internal/completion"
)

// Help is laid out as commander 14 laid out the TypeScript CLI's help: the
// usage line, the description, then Arguments, Options, Global Options and
// Commands, each item's description wrapped beside its term.
const (
	helpItemIndent  = 2
	helpSpacer      = 2
	helpMinWrap     = 40
	helpOptionTerm  = "-h, --help"
	helpDescription = "display help for command"
	helpCommandTerm = "help [command]"
)

// printHelp writes cmd's help, and the text it shows below it, to w.
func printHelp(cmd *cobra.Command, w io.Writer) {
	fmt.Fprint(w, formatHelp(cmd, helpWidth(w)))

	if after := shared.HelpAfterText(cmd); after != "" {
		fmt.Fprintln(w, after)
	}
}

// helpWidth is the terminal's width, or 80 when w is not a terminal.
func helpWidth(w io.Writer) int {
	file, isFile := w.(*os.File)

	if isFile && term.IsTerminal(int(file.Fd())) {
		if width, _, err := term.GetSize(int(file.Fd())); err == nil && width > 0 {
			return width
		}
	}

	return 80
}

type helpItem struct {
	term        string
	description string
}

func formatHelp(cmd *cobra.Command, width int) string {
	arguments := argumentItems(cmd)
	options := append(optionItems(cmd.LocalFlags()), helpItem{helpOptionTermFor(cmd), helpDescription})
	globals := globalOptionItems(cmd)
	commands := commandItems(cmd)

	termWidth := 0

	for _, items := range [][]helpItem{arguments, options, globals, commands} {
		for _, item := range items {
			termWidth = max(termWidth, shared.UTF16Length(item.term))
		}
	}

	output := []string{"Usage: " + commandUsage(cmd), ""}

	if description := commandDescription(cmd); description != "" {
		output = append(output, boxWrap(description, width), "")
	}

	sections := []struct {
		heading string
		items   []helpItem
	}{
		{"Arguments:", arguments},
		{"Options:", options},
		{"Global Options:", globals},
		{"Commands:", commands},
	}

	for _, section := range sections {
		if len(section.items) == 0 {
			continue
		}

		output = append(output, section.heading)

		for _, item := range section.items {
			output = append(output, formatItem(item, termWidth, width))
		}

		output = append(output, "")
	}

	return strings.Join(output, "\n")
}

// helpOptionTermFor drops -h where one of cmd's own options already uses it.
func helpOptionTermFor(cmd *cobra.Command) string {
	isTaken := false

	cmd.LocalFlags().VisitAll(func(flag *pflag.Flag) {
		isTaken = isTaken || (flag.Name != "help" && flag.Shorthand == "h")
	})

	if isTaken {
		return "--help"
	}

	return helpOptionTerm
}

// commandUsage is the command's path with commander's usage after its name:
// [options], [command] for a group, then its arguments.
func commandUsage(cmd *cobra.Command) string {
	name := cmd.Name()

	if len(cmd.Aliases) > 0 {
		name += "|" + cmd.Aliases[0]
	}

	path := name

	for parent := cmd.Parent(); parent != nil; parent = parent.Parent() {
		path = parent.Name() + " " + path
	}

	usage := shared.UsageText(cmd)

	if usage == "" {
		parts := []string{"[options]"}

		if cmd.HasSubCommands() {
			parts = append(parts, "[command]")
		}

		parts = append(parts, argumentTerms(cmd)...)
		usage = strings.Join(parts, " ")
	}

	return path + " " + usage
}

func commandDescription(cmd *cobra.Command) string {
	if cmd.Long != "" {
		return cmd.Long
	}

	return cmd.Short
}

// argumentTerms are the arguments cmd.Use names: <required>, [optional], <rest...>.
func argumentTerms(cmd *cobra.Command) []string {
	return strings.Fields(cmd.Use)[1:]
}

// argumentItems are listed only when one of them has a description.
func argumentItems(cmd *cobra.Command) []helpItem {
	declared := completion.ArgsOf(cmd)
	isDescribed := false

	for _, arg := range declared {
		isDescribed = isDescribed || arg.Description != ""
	}

	if !isDescribed {
		return nil
	}

	items := make([]helpItem, len(declared))

	for i, arg := range declared {
		description := arg.Description

		if arg.Choices != nil {
			description = withExtra(description, "choices: "+quotedList(arg.Choices))
		}

		items[i] = helpItem{arg.Name, description}
	}

	return items
}

func optionItems(flags *pflag.FlagSet) []helpItem {
	var items []helpItem

	flags.VisitAll(func(flag *pflag.Flag) {
		if flag.Hidden || flag.Name == "help" {
			return
		}

		var extra []string

		if values := completion.FlagChoices(flag); values != nil {
			extra = append(extra, "choices: "+quotedList(values))
		}

		if shown := shared.DefaultText(flag); shown != "" {
			extra = append(extra, "default: "+shown)
		}

		description := flag.Usage

		if len(extra) > 0 {
			description = withExtra(description, strings.Join(extra, ", "))
		}

		items = append(items, helpItem{shared.Spec(flag), description})
	})

	return items
}

// globalOptionItems are the options of the commands above cmd, nearest first.
func globalOptionItems(cmd *cobra.Command) []helpItem {
	var items []helpItem

	for parent := cmd.Parent(); parent != nil; parent = parent.Parent() {
		items = append(items, optionItems(parent.LocalFlags())...)
	}

	return items
}

// commandItems are the visible subcommands, then help [command] for a group.
func commandItems(cmd *cobra.Command) []helpItem {
	if !cmd.HasSubCommands() {
		return nil
	}

	var items []helpItem

	for _, child := range cmd.Commands() {
		if child.Hidden {
			continue
		}

		term := child.Name()

		if len(child.Aliases) > 0 {
			term += "|" + child.Aliases[0]
		}

		if hasOptions(child) {
			term += " [options]"
		}

		if args := argumentTerms(child); len(args) > 0 {
			term += " " + strings.Join(args, " ")
		}

		items = append(items, helpItem{term, child.Short})
	}

	items = append(items, helpItem{helpCommandTerm, helpDescription})

	return items
}

func hasOptions(cmd *cobra.Command) bool {
	isFound := false

	cmd.LocalFlags().VisitAll(func(flag *pflag.Flag) {
		isFound = isFound || flag.Name != "help"
	})

	return isFound
}

func withExtra(description, extra string) string {
	if description == "" {
		return "(" + extra + ")"
	}

	return description + " (" + extra + ")"
}

func quotedList(values []string) string {
	quoted := make([]string, len(values))

	for i, value := range values {
		encoded, _ := json.Marshal(value)
		quoted[i] = string(encoded)
	}

	return strings.Join(quoted, ", ")
}

var preformatted = regexp.MustCompile(`\n[^\S\r\n]`)

// formatItem pads the term and wraps the description in the space beside it.
func formatItem(item helpItem, termWidth, width int) string {
	indent := strings.Repeat(" ", helpItemIndent)

	if item.description == "" {
		return indent + item.term
	}

	padding := strings.Repeat(" ", termWidth-shared.UTF16Length(item.term))
	remaining := width - termWidth - helpSpacer - helpItemIndent
	description := item.description

	if remaining >= helpMinWrap && !preformatted.MatchString(description) {
		wrapped := boxWrap(description, remaining)
		description = strings.ReplaceAll(wrapped, "\n", "\n"+strings.Repeat(" ", termWidth+helpSpacer))
	}

	return indent + item.term + padding + strings.Repeat(" ", helpSpacer) + strings.ReplaceAll(description, "\n", "\n"+indent)
}

var chunks = regexp.MustCompile(`\s*\S+`)

// boxWrap breaks each line before the word that would pass width.
func boxWrap(text string, width int) string {
	if width < helpMinWrap {
		return text
	}

	var wrapped []string

	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		words := chunks.FindAllString(line, -1)

		if words == nil {
			wrapped = append(wrapped, "")
			continue
		}

		current := words[0]
		currentWidth := shared.UTF16Length(current)

		for _, word := range words[1:] {
			wordWidth := shared.UTF16Length(word)

			if currentWidth+wordWidth <= width {
				current += word
				currentWidth += wordWidth
				continue
			}

			wrapped = append(wrapped, current)
			current = strings.TrimLeft(word, " \t\n\r\f\v")
			currentWidth = shared.UTF16Length(current)
		}

		wrapped = append(wrapped, current)
	}

	return strings.Join(wrapped, "\n")
}

// newHelpCommand is commander's help [command]: the first word names the
// command; an unknown one shows this command's help as an error.
func newHelpCommand(root *cobra.Command) *cobra.Command {
	return &cobra.Command{
		Use:    "help",
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return helpAsError(root, append([]string{"help"}, args...))
		},
	}
}
