// Package completion completes command lines for the bash and zsh scripts.
// Commands declare a completer where they define the flag or argument.
package completion

import (
	"strings"
	"sync"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"featherless.ai/featherless-cloud/internal/api"
)

// Prefix of a volume path: vol://<volume>/<path>.
const Scheme = "vol://"

type Context struct {
	// Profile is the --profile value on the line being completed.
	Profile string
	// Args are the positional values typed so far, e.g. the job id for
	// `job logs <id> --attempt`.
	Args []string
	// Cloud returns a client authenticated for Profile, created once per Tab.
	Cloud func() (api.Cloud, error)
}

// Completer returns candidates for the word being typed; none lets the shell
// complete local files.
type Completer func(word string, ctx *Context) ([]string, error)

// Arg declares a positional argument for help and completion.
type Arg struct {
	Name     string
	Variadic bool
	Complete Completer
	Choices  []string
}

var (
	mu        sync.RWMutex
	flags     = map[*pflag.Flag]Completer{}
	choices   = map[*pflag.Flag][]string{}
	arguments = map[*cobra.Command][]Arg{}
)

// Flag attaches a completer to the named flag of cmd.
func Flag(cmd *cobra.Command, name string, complete Completer) {
	flag := lookup(cmd, name)
	mu.Lock()
	defer mu.Unlock()
	flags[flag] = complete
}

// Choices restricts the named flag to fixed values, which also complete.
func Choices(cmd *cobra.Command, name string, values ...string) {
	flag := lookup(cmd, name)
	mu.Lock()
	defer mu.Unlock()
	choices[flag] = values
}

// Args declares cmd's positional arguments, in order.
func Args(cmd *cobra.Command, args ...Arg) {
	mu.Lock()
	defer mu.Unlock()
	arguments[cmd] = args
}

func lookup(cmd *cobra.Command, name string) *pflag.Flag {
	flag := cmd.Flags().Lookup(name)

	if flag == nil {
		flag = cmd.PersistentFlags().Lookup(name)
	}

	if flag == nil {
		panic("completion: " + cmd.CommandPath() + " has no flag --" + name)
	}

	return flag
}

// FlagChoices returns the fixed values declared for flag, if any.
func FlagChoices(flag *pflag.Flag) []string {
	mu.RLock()
	defer mu.RUnlock()
	return choices[flag]
}

// FlagCompleter returns the declared completer, or the choices as one.
func FlagCompleter(flag *pflag.Flag) Completer {
	mu.RLock()
	complete, values := flags[flag], choices[flag]
	mu.RUnlock()

	if complete != nil {
		return complete
	}

	if values != nil {
		return ChoicesCompleter(values)
	}

	return nil
}

// ArgsOf returns cmd's declared positional arguments.
func ArgsOf(cmd *cobra.Command) []Arg {
	mu.RLock()
	defer mu.RUnlock()
	return arguments[cmd]
}

// ArgCompleter returns the argument's completer, or its choices as one.
func ArgCompleter(arg Arg) Completer {
	if arg.Complete != nil {
		return arg.Complete
	}

	if arg.Choices != nil {
		return ChoicesCompleter(arg.Choices)
	}

	return nil
}

func ChoicesCompleter(values []string) Completer {
	return func(word string, _ *Context) ([]string, error) {
		var out []string

		for _, value := range values {
			if strings.HasPrefix(value, word) {
				out = append(out, value)
			}
		}

		return out, nil
	}
}
