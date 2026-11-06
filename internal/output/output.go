// Package output renders command results in the three modes: JSON for
// scripts and agents, plain text, and the interactive terminal style.
package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"
	"golang.org/x/term"

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

func FormatTable(header []string, rows [][]string) string {
	widths := make([]int, len(header))

	for i, h := range header {
		widths[i] = utf8.RuneCountInString(h)

		for _, row := range rows {
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

	lines := []string{formatRow(header), strings.Join(dashes, "  ")}

	for _, row := range rows {
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

	text := FormatTable(result.Table.Header, result.Table.Rows)

	if c.Mode == Interactive {
		c.styled(styleInfo, text)

		if result.Footer != "" {
			c.styled(styleInfo, result.Footer)
		}

		return
	}

	fmt.Fprintln(c.Stdout, text)

	if result.Footer != "" {
		fmt.Fprintln(c.Stdout, result.Footer)
	}
}

func (c *Context) Log(message string) {
	switch c.Mode {
	case JSON:
	case Interactive:
		c.styled(styleInfo, message)
	default:
		fmt.Fprintln(c.Stdout, message)
	}
}

func (c *Context) Success(message string) {
	switch c.Mode {
	case JSON:
	case Interactive:
		c.styled(styleSuccess, message)
	default:
		fmt.Fprintln(c.Stdout, message)
	}
}

func (c *Context) Warn(message string) {
	switch c.Mode {
	case JSON:
	case Interactive:
		c.styled(styleWarn, message)
	default:
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

// Spin runs fn behind a spinner in interactive mode.
func Spin[T any](c *Context, label string, fn func() (T, error)) (T, error) {
	if c.Mode != Interactive {
		return fn()
	}

	stop := c.startSpinner(label)
	result, err := fn()

	if err != nil {
		stop("Failed.")
	} else {
		stop(label + " done.")
	}

	return result, err
}

type style struct {
	symbol string
	color  string
}

var (
	styleInfo    = style{"●", "\x1b[34m"}
	styleSuccess = style{"◆", "\x1b[32m"}
	styleWarn    = style{"▲", "\x1b[33m"}
	styleStep    = style{"◇", "\x1b[32m"}
)

func (c *Context) colors() bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}

	file, ok := c.Stdout.(*os.File)
	return ok && term.IsTerminal(int(file.Fd()))
}

func (c *Context) paint(color, text string) string {
	if !c.colors() {
		return text
	}

	return color + text + "\x1b[0m"
}

// styled prints one message the way the interactive terminal UI does: a bar
// line, then the symbol, with continuation lines under the bar.
func (c *Context) styled(s style, message string) {
	bar := c.paint("\x1b[90m", "│")
	lines := strings.Split(message, "\n")
	var out strings.Builder

	fmt.Fprintf(&out, "%s\n%s  %s\n", bar, c.paint(s.color, s.symbol), lines[0])

	for _, line := range lines[1:] {
		fmt.Fprintf(&out, "%s  %s\n", bar, line)
	}

	fmt.Fprint(c.Stdout, out.String())
}

func (c *Context) startSpinner(label string) func(string) {
	if !c.colors() {
		return func(message string) { c.styled(styleStep, message) }
	}

	frames := []string{"◒", "◐", "◓", "◑"}
	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		ticker := time.NewTicker(80 * time.Millisecond)
		defer ticker.Stop()

		for i := 0; ; i++ {
			fmt.Fprintf(c.Stdout, "\r\x1b[K%s  %s", c.paint("\x1b[35m", frames[i%len(frames)]), label)

			select {
			case <-done:
				return
			case <-ticker.C:
			}
		}
	}()

	return func(message string) {
		close(done)
		wg.Wait()
		fmt.Fprintf(c.Stdout, "\r\x1b[K%s  %s\n", c.paint(styleStep.color, styleStep.symbol), message)
	}
}
