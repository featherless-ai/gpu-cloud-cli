package main

import (
	"fmt"
	"os"
	"os/signal"
	"slices"
	"syscall"

	"featherless.ai/featherless-cloud/internal/commands"
	"featherless.ai/featherless-cloud/internal/commands/shared"
	"featherless.ai/featherless-cloud/internal/errs"
	"featherless.ai/featherless-cloud/internal/output"
	"featherless.ai/featherless-cloud/internal/update"
)

func main() {
	// A closed pipe (`featherless-cloud volume find … | head`) ends the output
	// with EPIPE instead of killing the process, as in the TypeScript CLI.
	signal.Ignore(syscall.SIGPIPE)
	stopOnInterrupt()
	os.Exit(run(os.Args[1:]))
}

// A script's background job starts with Ctrl-C ignored, yet Node still ends on
// it (exit 130), so scripts could stop the TypeScript CLI with kill -INT. Go
// keeps an inherited ignore, so the CLI handles Ctrl-C itself then, leaving it
// to commands that take it over (exec, ssh).
func stopOnInterrupt() {
	if !signal.Ignored(os.Interrupt) {
		return
	}

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt)

	go func() {
		for range signals {
			if !shared.SignalsTaken() {
				os.Exit(130)
			}
		}
	}()
}

func run(args []string) int {
	update.OfferUpdate(args)

	root := commands.NewRoot()
	root.SetArgs(args)
	err := root.Execute()

	if err == nil {
		return 0
	}

	if code, isExit := errs.ExitCode(err); isExit {
		return code
	}

	message := errs.Describe(err)

	if errs.IsKind(err, errs.KindUsage) {
		fmt.Fprintf(os.Stderr, "error: %s\n", message)
		return 1
	}

	wantsJSON, _ := root.PersistentFlags().GetBool("json")

	// A parse error stops before flags are read, so look at the words too,
	// but not past "--", where they belong to the remote command.
	options := args

	if end := slices.Index(args, "--"); end >= 0 {
		options = args[:end]
	}

	if wantsJSON || slices.Contains(options, "--json") {
		fmt.Println(output.EncodeJSON(map[string]string{"error": message}, false))
	} else {
		fmt.Fprintf(os.Stderr, "Error: %s\n", message)
	}

	return 1
}
