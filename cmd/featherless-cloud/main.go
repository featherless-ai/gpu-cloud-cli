package main

import (
	"fmt"
	"os"
	"os/signal"
	"slices"
	"syscall"

	"featherless.ai/featherless-cloud/internal/commands"
	"featherless.ai/featherless-cloud/internal/errs"
	"featherless.ai/featherless-cloud/internal/output"
	"featherless.ai/featherless-cloud/internal/update"
)

func main() {
	// A closed pipe (`featherless-cloud volume find … | head`) ends the output
	// with EPIPE instead of killing the process, as in the TypeScript CLI.
	signal.Ignore(syscall.SIGPIPE)
	os.Exit(run(os.Args[1:]))
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
