package completion

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"featherless.ai/featherless-cloud/internal/api"
	"featherless.ai/featherless-cloud/internal/version"
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

var helpOption = Option{Long: "--help"}

// CommandTree builds the tree the scripts complete: hidden commands excluded,
// inherited flags and --help on every command, aliases and help as commands.
func CommandTree(root *cobra.Command) *Node {
	return commandTree(root, nil)
}

// commandTree gives cmd the persistent flags of its ancestors, in inherited,
// before its own; a parent's local flags don't parse on its children.
func commandTree(cmd *cobra.Command, inherited []Option) *Node {
	var own []Option
	var persistent []Option

	cmd.LocalFlags().VisitAll(func(flag *pflag.Flag) {
		if flag.Name == "help" {
			return
		}

		option := optionOf(flag)
		own = append(own, option)

		if cmd.PersistentFlags().Lookup(flag.Name) == flag {
			persistent = append(persistent, option)
		}
	})

	options := dedupeOptions(inherited, own, []Option{helpOption})
	passedDown := dedupeOptions(inherited, persistent, []Option{helpOption})

	name := cmd.Name()

	if name == "" {
		name = version.AppName
	}

	node := &Node{Name: name, Options: options}

	for _, arg := range ArgsOf(cmd) {
		argument := Argument{Variadic: arg.Variadic, Complete: ArgCompleter(arg)}
		node.Arguments = append(node.Arguments, argument)
	}

	// IsAvailableCommand also leaves out cobra's own help command.
	for _, command := range cmd.Commands() {
		if !command.IsAvailableCommand() {
			continue
		}

		child := commandTree(command, passedDown)
		node.Commands = append(node.Commands, child)

		for _, alias := range command.Aliases {
			aliased := *child
			aliased.Name = alias
			node.Commands = append(node.Commands, &aliased)
		}
	}

	if len(node.Commands) > 0 {
		help := &Node{Name: "help", Options: options, HelpFor: node}
		node.Commands = append(node.Commands, help)
	}

	return node
}

func optionOf(flag *pflag.Flag) Option {
	option := Option{
		Long:       "--" + flag.Name,
		TakesValue: flag.NoOptDefVal == "",
		Complete:   FlagCompleter(flag),
	}

	if flag.Shorthand != "" {
		option.Short = "-" + flag.Shorthand
	}

	return option
}

func dedupeOptions(groups ...[]Option) []Option {
	var options []Option
	seen := map[string]bool{}

	for _, group := range groups {
		for _, option := range group {
			if seen[option.Long] {
				continue
			}

			seen[option.Long] = true
			options = append(options, option)
		}
	}

	return options
}

// TokenizeLine splits a command line into words with escapes and quotes
// decoded. open means the last word is still being typed.
func TokenizeLine(line string) (words []string, open bool) {
	var word strings.Builder
	var quote rune
	isStarted := false
	isEscaped := false

	for _, c := range line {
		switch {
		case isEscaped:
			word.WriteRune(c)
			isStarted = true
			isEscaped = false
		case c == '\\':
			isEscaped = true
		case quote != 0:
			if c == quote {
				quote = 0
			} else {
				word.WriteRune(c)
			}
		case c == '"' || c == '\'':
			quote = c
			isStarted = true
		case c == ' ' || c == '\t':
			if isStarted {
				words = append(words, word.String())
				word.Reset()
				isStarted = false
			}
		default:
			word.WriteRune(c)
			isStarted = true
		}
	}

	if isStarted {
		words = append(words, word.String())
	}

	return words, isStarted
}

// WalkCommandTree resolves the words after the program name to the deepest
// command, a flag awaiting its value, and the positional values.
func WalkCommandTree(tree *Node, words []string) Position {
	node := tree
	var afterFlag *Option
	var args []string
	isLiteral := false

	for _, word := range words {
		if afterFlag != nil {
			afterFlag = nil
			continue
		}

		if isLiteral {
			args = append(args, word)
			continue
		}

		child := findChild(node, word)

		if child != nil {
			node = child
			args = nil
			continue
		}

		if word == "--" {
			isLiteral = true
			continue
		}

		if strings.HasPrefix(word, "--") {
			if !strings.Contains(word, "=") {
				afterFlag = findValueOption(node, func(option *Option) bool {
					return option.Long == word
				})
			}

			continue
		}

		if strings.HasPrefix(word, "-") && len(word) > 1 {
			afterFlag = findValueOption(node, func(option *Option) bool {
				return option.Short == word
			})

			continue
		}

		args = append(args, word)
	}

	return Position{Node: node, AfterFlag: afterFlag, Args: args}
}

func findChild(node *Node, name string) *Node {
	for _, command := range node.Commands {
		if command.Name == name {
			return command
		}
	}

	return nil
}

func findValueOption(node *Node, matches func(option *Option) bool) *Option {
	for index := range node.Options {
		option := &node.Options[index]

		if option.TakesValue && matches(option) {
			return option
		}
	}

	return nil
}

// CompleteWord returns candidates for current from the completers the command
// declares; none lets the shell complete local files.
func CompleteWord(current string, position Position, ctx *Context) ([]string, error) {
	node := position.Node

	if position.AfterFlag != nil {
		if position.AfterFlag.Complete == nil {
			return nil, nil
		}

		return position.AfterFlag.Complete(current, ctx)
	}

	flag, value, hasValue := strings.Cut(current, "=")
	isAssignment := hasValue && strings.HasPrefix(flag, "--") && len(flag) > 2

	if isAssignment {
		var complete Completer

		for _, option := range node.Options {
			if option.Long == flag {
				complete = option.Complete
				break
			}
		}

		if complete == nil {
			return nil, nil
		}

		values, err := complete(value, ctx)
		if err != nil {
			return nil, err
		}

		var assignments []string

		for _, candidate := range values {
			assignments = append(assignments, flag+"="+candidate)
		}

		return assignments, nil
	}

	if strings.HasPrefix(current, "-") {
		var flags []string
		seen := map[string]bool{}

		for _, option := range node.Options {
			for _, name := range []string{option.Short, option.Long} {
				if name == "" || seen[name] {
					continue
				}

				seen[name] = true

				if strings.HasPrefix(name, current) {
					flags = append(flags, name)
				}
			}
		}

		return flags, nil
	}

	target := node

	if node.HelpFor != nil {
		target = node.HelpFor
	}

	if len(position.Args) == 0 {
		var commands []string

		for _, command := range target.Commands {
			if strings.HasPrefix(command.Name, current) {
				commands = append(commands, command.Name)
			}
		}

		if len(commands) > 0 {
			return commands, nil
		}
	}

	var argument *Argument
	count := len(position.Args)

	if count < len(node.Arguments) {
		argument = &node.Arguments[count]
	} else if len(node.Arguments) > 0 && node.Arguments[len(node.Arguments)-1].Variadic {
		argument = &node.Arguments[len(node.Arguments)-1]
	}

	if argument == nil || argument.Complete == nil {
		return nil, nil
	}

	return argument.Complete(current, ctx)
}

// ProfileFromCommand returns the last --profile value on the raw command
// line, or "" when there is none.
func ProfileFromCommand(line string) string {
	words, _ := TokenizeLine(line)

	for index := len(words) - 1; index >= 1; index-- {
		word := words[index]

		if word == "--profile" {
			if index+1 < len(words) {
				return words[index+1]
			}

			return ""
		}

		if value, found := strings.CutPrefix(word, "--profile="); found {
			return value
		}
	}

	return ""
}

// Readline exports COMP_LINE/COMP_POINT to the function's environment; the
// CLI tokenizes the whole line for command/flag context.
//
// Readline replaces only the part of the word after its last unquoted
// COMP_WORDBREAKS `:` or `=` (`vol:` + `//data/…`, `--profile=` + `prod`),
// or after an open quote. The scan finds that point as `brk`, and the text
// before it comes off every candidate. Filename quoting would insert the
// bare scheme as `vol\://`.
//
// Readline lays matches out in columns; padding each past half the screen
// lists one per row. Paths keep columns: under -o filenames readline shows
// only the text after their last `/`, which padding would blank.
const bashTemplate = `{fn}() {
  local line cur prefix c i q qpos esc2 start brk fc breaks pad
  breaks=${COMP_WORDBREAKS//[^:=]/}
  line=${COMP_LINE:0:$COMP_POINT}
  i=0 q= esc2=0 start=-1 brk=-1
  while [ $i -lt ${#line} ]; do
    c=${line:$i:1}
    if [ $esc2 -eq 1 ]; then esc2=0
    elif [ "$c" = '\' ]; then esc2=1
    elif [ -n "$q" ] && [ "$c" = "$q" ]; then q=
    elif [ -z "$q" ]; then
      case $c in
        \"|\') q=$c qpos=$i ;;
        ' '|$'\t') start=$i brk=$i ;;
        *) [ -n "$breaks" ] && [[ $breaks == *"$c"* ]] && brk=$i ;;
      esac
    fi
    i=$((i+1))
  done
  [ -n "$q" ] && brk=$qpos
  cur=${line:$((start + 1))}
  prefix=${line:$((start + 1)):$((brk - start))}
  cur=${cur//\\} prefix=${prefix//\\}
  cur=${cur//\"/} prefix=${prefix//\"/}
  cur=${cur//\'/} prefix=${prefix//\'/}
  COMPREPLY=()
  while IFS= read -r fc; do
    COMPREPLY+=("$fc")
  done < <(COMP_LINE="$COMP_LINE" COMP_POINT="$COMP_POINT" FC_WORD="$cur" {app} _complete 2>/dev/null)
  [ -n "$prefix" ] && COMPREPLY=("${COMPREPLY[@]#"$prefix"}")
  [ "${COMPREPLY[*]}" = '{scheme}' ] && compopt +o filenames 2>/dev/null
  if [ ${#COMPREPLY[@]} -gt 1 ] && [[ ${COMPREPLY[*]} != */* ]]; then
    pad=$(printf "%$(( ${COLUMNS:-$(tput cols 2>/dev/null || echo 80)} / 2 ))s" '')
    for i in "${!COMPREPLY[@]}"; do
      COMPREPLY[i]=${COMPREPLY[i]}${pad:${#COMPREPLY[i]}}
    done
  fi
  if [ ${#COMPREPLY[@]} -gt 0 ]; then
    for fc in "${COMPREPLY[@]}"; do
      case $fc in */|*:) ;; *) return 0 ;; esac
    done

    compopt -o nospace 2>/dev/null
  fi
}
complete -o default -o filenames -F {fn} {app}
`

func BashScript() string {
	fn := "_" + strings.ReplaceAll(version.AppName, "-", "_") + "_completions"
	replacer := strings.NewReplacer("{fn}", fn, "{app}", version.AppName, "{scheme}", Scheme)

	return replacer.Replace(bashTemplate)
}

// compinit autoloads the installed file (~/.zsh/completions/_featherless-cloud)
// as the function body, so the first call defines the function and must also
// run it; `eval "$(... completion zsh)"` registers it with compdef. The words
// before the cursor and ${(Q)PREFIX}, the in-progress word unquoted, go
// through the environment. compadd quotes the full-path candidates on
// insertion; -d lists only their last path component, and -l lists one per row.
const zshTemplate = `#compdef {app}
{fn}() {
  local -a cand nospace nospaceshown plain plainshown
  local c shown ret=1
  cand=( ${(f)"$(COMP_LINE="${words[1,CURRENT-1]} " FC_WORD=${(Q)PREFIX} {app} _complete 2>/dev/null)"} )
  if (( ! ${#cand} )); then
    _files
    return
  fi
  for c in "${cand[@]}"; do
    shown=$c
    if [[ $c == vol://?*/* ]]; then
      shown=${${c%/}##*/}
      [[ $c == */ ]] && shown+=/
    fi
    if [[ $c == */ || $c == *: ]]; then
      nospace+=("$c") nospaceshown+=("$shown")
    else
      plain+=("$c") plainshown+=("$shown")
    fi
  done
  (( ${#nospace} )) && compadd -l -S '' -d nospaceshown -- "${nospace[@]}" && ret=0
  (( ${#plain} )) && compadd -l -d plainshown -- "${plain[@]}" && ret=0
  return ret
}
if [[ ${funcstack[1]} == {fn} ]]; then
  {fn} "$@"
else
  compdef {fn} {app}
fi
`

func ZshScript() string {
	fn := "_" + version.AppName
	replacer := strings.NewReplacer("{fn}", fn, "{app}", version.AppName)

	return replacer.Replace(zshTemplate)
}

// Run is the hidden _complete command: it reads COMP_LINE, COMP_POINT and
// FC_WORD through getenv and prints one candidate per line. Failures print
// nothing, so the shell falls back to completing local files.
func Run(root *cobra.Command, getenv func(string) string, newCloud NewCloud, out io.Writer) {
	line := getenv("COMP_LINE")
	point, err := strconv.Atoi(getenv("COMP_POINT"))

	if err == nil && point >= 0 {
		characters := []rune(line)

		if point < len(characters) {
			line = string(characters[:point])
		}
	}

	words, open := TokenizeLine(line)
	end := len(words)

	if open {
		end--
	}

	// FC_WORD carries the in-progress word; words starting with `-` would be
	// parsed as flags if passed as arguments.
	current := getenv("FC_WORD")

	if current == "" && open {
		current = words[len(words)-1]
	}

	var commandWords []string

	if end > 1 {
		commandWords = words[1:end]
	}

	position := WalkCommandTree(CommandTree(root), commandWords)
	profile := ProfileFromCommand(line)

	ctx := &Context{
		Profile: profile,
		Args:    position.Args,
		Cloud: sync.OnceValues(func() (api.Cloud, error) {
			return newCloud(profile)
		}),
	}

	candidates, err := CompleteWord(current, position, ctx)
	if err != nil {
		return
	}

	for _, candidate := range candidates {
		fmt.Fprintln(out, candidate)
	}
}
