package output

import (
	"errors"

	"github.com/charmbracelet/huh"
)

// ErrCancelled means the person cancelled a prompt. Commands print
// "Cancelled." and exit 0.
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

type huhPrompter struct{}

func run(field huh.Field) error {
	err := huh.NewForm(huh.NewGroup(field)).WithShowHelp(false).Run()

	if errors.Is(err, huh.ErrUserAborted) {
		return ErrCancelled
	}

	return err
}

func (huhPrompter) Text(message, placeholder, initial string, validate func(string) error) (string, error) {
	value := initial
	input := huh.NewInput().Title(message).Placeholder(placeholder).Value(&value)

	if validate != nil {
		input = input.Validate(validate)
	}

	return value, run(input)
}

func (huhPrompter) Password(message string, validate func(string) error) (string, error) {
	var value string
	input := huh.NewInput().Title(message).EchoMode(huh.EchoModePassword).Value(&value)

	if validate != nil {
		input = input.Validate(validate)
	}

	return value, run(input)
}

func (huhPrompter) Confirm(message string, initial bool) (bool, error) {
	value := initial
	return value, run(huh.NewConfirm().Title(message).Value(&value))
}

func (huhPrompter) Select(message string, options []Option, initial string) (string, error) {
	value := initial
	choices := make([]huh.Option[string], len(options))

	for i, option := range options {
		label := option.Label

		if option.Hint != "" {
			label += " (" + option.Hint + ")"
		}

		choices[i] = huh.NewOption(label, option.Value)
	}

	return value, run(huh.NewSelect[string]().Title(message).Options(choices...).Value(&value))
}
