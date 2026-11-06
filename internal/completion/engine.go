package completion

import (
	"io"

	"github.com/spf13/cobra"

	"featherless.ai/featherless-cloud/internal/api"
)

// NewCloud makes the client completers use for a --profile value.
type NewCloud func(profile string) (api.Cloud, error)

// Option is a flag as the completion tree sees it.
type Option struct {
	// Long is the --name form, Short the -x form when the flag has one.
	Long  string
	Short string
	// TakesValue means the next word is the flag's value.
	TakesValue bool
	Complete   Completer
}

// Argument is a positional argument as the completion tree sees it.
type Argument struct {
	Variadic bool
	Complete Completer
}

// Node is a command in the tree the shell scripts complete.
type Node struct {
	Name      string
	Options   []Option
	Arguments []Argument
	Commands  []*Node
	// HelpFor is set on the help node: the command whose subcommands help lists.
	HelpFor *Node
}

// Position is where a command line leaves the word being completed.
type Position struct {
	Node *Node
	// AfterFlag is the option before the word being completed, awaiting its value.
	AfterFlag *Option
	// Args are the positional values before the word being completed.
	Args []string
}

// CommandTree builds the tree the scripts complete: hidden commands excluded,
// inherited flags and --help on every command, aliases and help as commands.
func CommandTree(root *cobra.Command) *Node {
	return &Node{}
}

// TokenizeLine splits a command line into words with escapes and quotes
// decoded. open means the last word is still being typed.
func TokenizeLine(line string) (words []string, open bool) {
	return nil, false
}

// WalkCommandTree resolves the words after the program name to the deepest
// command, a flag awaiting its value, and the positional values.
func WalkCommandTree(tree *Node, words []string) Position {
	return Position{Node: tree}
}

// CompleteWord returns candidates for current from the completers the command
// declares; none lets the shell complete local files.
func CompleteWord(current string, position Position, ctx *Context) ([]string, error) {
	return nil, nil
}

// ProfileFromCommand returns the last --profile value on the raw command
// line, or "" when there is none.
func ProfileFromCommand(line string) string {
	return ""
}

func BashScript() string { return "" }

func ZshScript() string { return "" }

// Run is the hidden _complete command: it reads COMP_LINE, COMP_POINT and
// FC_WORD through getenv and prints one candidate per line. Failures print
// nothing, so the shell falls back to completing local files.
func Run(root *cobra.Command, getenv func(string) string, newCloud NewCloud, out io.Writer) {}
