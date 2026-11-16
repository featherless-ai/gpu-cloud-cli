// Package output renders command results as JSON for scripts and agents, or as
// plain text. Interactive mode prints the same text as plain mode and may also
// ask questions at a prompt.
package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"

	"featherless.ai/featherless-cloud/internal/errs"
)

type Mode int

const (
	Interactive Mode = iota
	Plain
	JSON
)

type Table struct {
	Header []string
	Rows   [][]string
}

// Result is a formatter's text form: a table (with optional footer) or text.
type Result struct {
	Table  *Table
	Footer string
	Text   string
}

type Context struct {
	Mode   Mode
	Stdout io.Writer
	Stderr io.Writer
	Stdin  io.Reader
	Prompt Prompter
	// Now is the clock for progress output; tests replace it.
	Now func() time.Time
}

func New(mode Mode) *Context {
	return &Context{Mode: mode, Stdout: os.Stdout, Stderr: os.Stderr, Stdin: os.Stdin, Prompt: huhPrompter{}, Now: time.Now}
}

// FromCommand reads --json and --non-interactive from the root command.
func FromCommand(cmd *cobra.Command) *Context {
	root := cmd.Root()
	asJSON, _ := root.PersistentFlags().GetBool("json")
	nonInteractive, _ := root.PersistentFlags().GetBool("non-interactive")

	switch {
	case asJSON:
		return New(JSON)
	case nonInteractive:
		return New(Plain)
	default:
		return New(Interactive)
	}
}

// EncodeJSON matches JSON.stringify: no HTML escaping, optional 2-space indent.
func EncodeJSON(data any, indent bool) string {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)

	if indent {
		encoder.SetIndent("", "  ")
	}

	if err := encoder.Encode(data); err != nil {
		return "null"
	}

	return strings.TrimSuffix(buf.String(), "\n")
}

func FormatTable(table Table) string {
	widths := make([]int, len(table.Header))

	for i, h := range table.Header {
		widths[i] = utf8.RuneCountInString(h)

		for _, row := range table.Rows {
			if i < len(row) {
				widths[i] = max(widths[i], utf8.RuneCountInString(row[i]))
			}
		}
	}

	formatRow := func(row []string) string {
		cells := make([]string, len(row))

		for i, cell := range row {
			width := 0

			if i < len(widths) {
				width = widths[i]
			}

			cells[i] = cell + strings.Repeat(" ", max(0, width-utf8.RuneCountInString(cell)))
		}

		return strings.Join(cells, "  ")
	}

	dashes := make([]string, len(widths))

	for i, w := range widths {
		dashes[i] = strings.Repeat("-", w)
	}

	lines := []string{formatRow(table.Header), strings.Join(dashes, "  ")}

	for _, row := range table.Rows {
		lines = append(lines, formatRow(row))
	}

	return strings.Join(lines, "\n")
}

func (c *Context) Output(data any, format func() Result) {
	if c.Mode == JSON {
		fmt.Fprintln(c.Stdout, EncodeJSON(data, true))
		return
	}

	result := format()

	if result.Table == nil {
		c.Log(result.Text)
		return
	}

	// At a terminal a blank line sets the table off from the command above it
	// and from the footer; plain mode keeps the lines scripts count on.
	gap := ""

	if c.Mode == Interactive {
		gap = "\n"
	}

	fmt.Fprint(c.Stdout, gap+FormatTable(*result.Table)+"\n")

	if result.Footer != "" {
		fmt.Fprint(c.Stdout, gap+result.Footer+"\n")
	}
}

func (c *Context) Log(message string) {
	if c.Mode != JSON {
		fmt.Fprintln(c.Stdout, message)
	}
}

func (c *Context) Success(message string) {
	c.Log(message)
}

func (c *Context) Warn(message string) {
	if c.Mode != JSON {
		fmt.Fprintf(c.Stderr, "Warning: %s\n", message)
	}
}

func (c *Context) Error(message string) {
	if c.Mode == JSON {
		fmt.Fprintln(c.Stdout, EncodeJSON(map[string]string{"error": message}, false))
		return
	}

	fmt.Fprintf(c.Stderr, "Error: %s\n", message)
}

// Fatal shows the error and returns an exit-1 error for the command to return.
func (c *Context) Fatal(message string) error {
	c.Error(message)
	return &errs.Exit{Code: 1}
}
