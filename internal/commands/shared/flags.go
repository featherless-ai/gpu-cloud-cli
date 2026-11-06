package shared

import (
	"encoding/json"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"featherless.ai/featherless-cloud/internal/errs"
)

// Option adds a flag that takes a value, declared as the TypeScript CLI
// declared it ("-i, --identity <file>"), so help names the value <file>.
func Option(cmd *cobra.Command, target *string, spec, value, usage string) {
	name, shorthand, placeholder := parseSpec(spec)
	*target = value
	flags := cmd.Flags()
	flags.SortFlags = false
	flags.VarP(&textValue{target: target, placeholder: placeholder}, name, shorthand, usage)

	if value != "" {
		encoded, _ := json.Marshal(value)
		_ = flags.SetAnnotation(name, defaultAnnotation, []string{string(encoded)})
	}
}

// Repeatable adds a flag that collects every value given, in order: "--env <K=V>".
func Repeatable(cmd *cobra.Command, target *[]string, spec, usage string) {
	name, shorthand, placeholder := parseSpec(spec)
	*target = []string{}
	flags := cmd.Flags()
	flags.SortFlags = false
	flags.VarP(&listValue{target: target, placeholder: placeholder}, name, shorthand, usage)
	_ = flags.SetAnnotation(name, defaultAnnotation, []string{"[]"})
}

// Collect is Repeatable for a flag the TypeScript CLI declared without a
// default, so help shows none: "--size <+n|-n>".
func Collect(cmd *cobra.Command, target *[]string, spec, usage string) {
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

// What help shows beyond a command's flags and arguments.
const (
	defaultAnnotation   = "featherless-cloud/default"
	usageAnnotation     = "featherless-cloud/usage"
	helpAfterAnnotation = "featherless-cloud/help-after"
)

// HelpAfter prints text below the command's help.
func HelpAfter(cmd *cobra.Command, text string) {
	annotate(cmd, helpAfterAnnotation, text)
}

// HelpAfterText is the text HelpAfter put below cmd's help.
func HelpAfterText(cmd *cobra.Command) string {
	return cmd.Annotations[helpAfterAnnotation]
}

// Usage replaces what help shows after the command's name, e.g.
// "[options] <sources...> <destination>" for arguments read as a list.
func Usage(cmd *cobra.Command, text string) {
	annotate(cmd, usageAnnotation, text)
}

// UsageText is the usage Usage set for cmd, if any.
func UsageText(cmd *cobra.Command) string {
	return cmd.Annotations[usageAnnotation]
}

// DefaultText is a flag's default as help shows it: "20", [] or nothing.
func DefaultText(flag *pflag.Flag) string {
	if values := flag.Annotations[defaultAnnotation]; len(values) > 0 {
		return values[0]
	}

	return ""
}

func annotate(cmd *cobra.Command, key, value string) {
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}

	cmd.Annotations[key] = value
}

// Arguments checks a command line in commander's order and words: required
// options first, then the positional arguments cmd.Use names (<required>,
// [optional], name... for the rest).
func Arguments(cmd *cobra.Command, args []string) error {
	var missing *pflag.Flag

	cmd.Flags().VisitAll(func(flag *pflag.Flag) {
		_, isRequired := flag.Annotations[cobra.BashCompOneRequiredFlag]

		if missing == nil && isRequired && !flag.Changed {
			missing = flag
		}
	})

	if missing != nil {
		return errs.Usagef("required option '%s' not specified", Spec(missing))
	}

	declared := strings.Fields(cmd.Use)[1:]
	isVariadic := false

	for index, token := range declared {
		name := strings.Trim(token, "<>[]")
		isVariadic = strings.HasSuffix(name, "...")

		if strings.HasPrefix(token, "<") && index >= len(args) {
			return errs.Usagef("missing required argument '%s'", strings.TrimSuffix(name, "..."))
		}
	}

	if isVariadic || len(args) <= len(declared) {
		return nil
	}

	plural := "s"

	if len(declared) == 1 {
		plural = ""
	}

	subcommand := ""

	if cmd.HasParent() {
		subcommand = " for '" + cmd.Name() + "'"
	}

	return errs.Usagef("too many arguments%s. Expected %d argument%s but got %d.", subcommand, len(declared), plural, len(args))
}

// Spec is a flag as declared, e.g. "-i, --identity <file>".
func Spec(flag *pflag.Flag) string {
	spec := "--" + flag.Name

	if flag.Shorthand != "" {
		spec = "-" + flag.Shorthand + ", " + spec
	}

	if placeholder, _ := pflag.UnquoteUsage(flag); placeholder != "" {
		spec += " " + placeholder
	}

	return spec
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
