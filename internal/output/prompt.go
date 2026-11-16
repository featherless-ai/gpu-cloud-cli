package output

import (
	"errors"
	"io"
	"os"

	"github.com/charmbracelet/huh"
	"golang.org/x/term"

	// Before Bubble Tea starts, so it never queries the terminal.
	_ "featherless.ai/featherless-cloud/internal/output/terminalquery"
)

func stdoutWidth() int {
	width, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		return 0
	}

	return width
}

// ErrCancelled means the person cancelled a prompt. Commands exit 0 quietly.
var ErrCancelled = errors.New("cancelled")

type Option struct {
	Label string
	Hint  string
	Value string
}

type Prompter interface {
	Text(message, placeholder, initial string, validate func(string) error) (string, error)
	Password(message string, validate func(string) error) (string, error)
	Confirm(message string, initial bool) (bool, error)
	Select(message string, options []Option, initial string) (string, error)
}

type huhPrompter struct {
	// in and out replace the terminal in tests.
	in  io.Reader
	out io.Writer
}

// newForm wraps one field. Without a terminal width (a pty that reports no
// size) the prompt library would size its fields from 0 and crash drawing them.
func newForm(field huh.Field, terminalWidth int) *huh.Form {
	form := huh.NewForm(huh.NewGroup(field)).WithShowHelp(false).WithTheme(promptTheme())

	if terminalWidth <= 0 {
		form = form.WithWidth(80)
	}

	return form
}

// promptTheme draws prompts in the terminal's own colours, which need no
// background colour and read on light and dark terminals alike. Text stays in
// the terminal's default colour, as the TypeScript CLI's prompts did.
func promptTheme() *huh.Theme {
	theme := huh.ThemeBase16()

	for _, styles := range []*huh.FieldStyles{&theme.Focused, &theme.Blurred} {
		styles.Option = styles.Option.UnsetForeground()
		styles.UnselectedOption = styles.UnselectedOption.UnsetForeground()
		styles.TextInput.Text = styles.TextInput.Text.UnsetForeground()
	}

	return theme
}

func (p huhPrompter) run(field huh.Field) error {
	form := newForm(field, stdoutWidth())

	if p.in != nil {
		form = form.WithInput(p.in)
	}

	if p.out != nil {
		form = form.WithOutput(p.out)
	}

	err := form.Run()

	if errors.Is(err, huh.ErrUserAborted) {
		return ErrCancelled
	}

	return err
}

func (p huhPrompter) Text(message, placeholder, initial string, validate func(string) error) (string, error) {
	value := initial
	input := huh.NewInput().Title(message).Placeholder(placeholder).Value(&value)

	if validate != nil {
		input = input.Validate(validate)
	}

	return value, p.run(input)
}

func (p huhPrompter) Password(message string, validate func(string) error) (string, error) {
	var value string
	input := huh.NewInput().Title(message).EchoMode(huh.EchoModePassword).Value(&value)

	if validate != nil {
		input = input.Validate(validate)
	}

	return value, p.run(input)
}

func (p huhPrompter) Confirm(message string, initial bool) (bool, error) {
	value := initial
	return value, p.run(huh.NewConfirm().Title(message).Value(&value))
}

func (p huhPrompter) Select(message string, options []Option, initial string) (string, error) {
	value := initial
	choices := make([]huh.Option[string], len(options))

	for i, option := range options {
		label := option.Label

		if option.Hint != "" {
			label += " (" + option.Hint + ")"
		}

		choices[i] = huh.NewOption(label, option.Value)
	}

	return value, p.run(huh.NewSelect[string]().Title(message).Options(choices...).Value(&value))
}
