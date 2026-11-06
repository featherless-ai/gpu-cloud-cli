package shared

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// Option adds a flag that takes a value, declared as the TypeScript CLI
// declared it ("-i, --identity <file>"), so help names the value <file>.
func Option(cmd *cobra.Command, target *string, spec, value, usage string) {
	name, shorthand, placeholder := parseSpec(spec)
	*target = value
	flags := cmd.Flags()
	flags.SortFlags = false
	flags.VarP(&textValue{target: target, placeholder: placeholder}, name, shorthand, usage)
}

// Repeatable adds a flag that collects every value given, in order: "--env <K=V>".
func Repeatable(cmd *cobra.Command, target *[]string, spec, usage string) {
	name, shorthand, placeholder := parseSpec(spec)
	*target = []string{}
	flags := cmd.Flags()
	flags.SortFlags = false
	flags.VarP(&listValue{target: target, placeholder: placeholder}, name, shorthand, usage)
}

// Switch adds a boolean flag: "--yes", "-f, --force" or "--no-retain-logs".
func Switch(cmd *cobra.Command, target *bool, spec, usage string) {
	name, shorthand, _ := parseSpec(spec)
	flags := cmd.Flags()
	flags.SortFlags = false
	flags.BoolVarP(target, name, shorthand, false, usage)
}

// HelpAfter prints text below the command's help.
func HelpAfter(cmd *cobra.Command, text string) {
	cmd.SetHelpFunc(func(c *cobra.Command, args []string) {
		help := (&cobra.Command{}).HelpFunc()

		if c.HasParent() {
			help = c.Parent().HelpFunc()
		}

		help(c, args)
		fmt.Fprintln(c.OutOrStdout(), text)
	})
}

func parseSpec(spec string) (name, shorthand, placeholder string) {
	for part := range strings.SplitSeq(spec, ", ") {
		switch {
		case strings.HasPrefix(part, "--"):
			long, value, _ := strings.Cut(strings.TrimPrefix(part, "--"), " ")
			name = long
			placeholder = value
		case strings.HasPrefix(part, "-"):
			shorthand = strings.TrimPrefix(part, "-")
		}
	}

	if name == "" {
		panic("shared: flag spec without a long name: " + spec)
	}

	return name, shorthand, placeholder
}

// textValue is a string flag whose type, which help shows, is its placeholder.
type textValue struct {
	target      *string
	placeholder string
}

func (v *textValue) String() string { return *v.target }

func (v *textValue) Set(value string) error {
	*v.target = value
	return nil
}

func (v *textValue) Type() string { return v.placeholder }

type listValue struct {
	target      *[]string
	placeholder string
}

func (v *listValue) String() string { return strings.Join(*v.target, ",") }

func (v *listValue) Set(value string) error {
	*v.target = append(*v.target, value)
	return nil
}

func (v *listValue) Type() string { return v.placeholder }
