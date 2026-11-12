package completion

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"featherless.ai/featherless-cloud/internal/api"
)

var datasets = api.NetworkVolume{ID: "11111111-2222-4333-8444-555555555555", Name: "datasets"}

// fakeCloud lists volumes as the account's volumes.
type fakeCloud struct {
	api.Cloud
	volumes []api.NetworkVolume
}

func (f *fakeCloud) ListVolumes(ctx context.Context) ([]api.NetworkVolume, error) {
	return f.volumes, nil
}

func withVolumes(volumes ...api.NetworkVolume) *fakeCloud {
	return &fakeCloud{volumes: volumes}
}

// completerContext is a completer context whose client is cloud.
func completerContext(cloud api.Cloud, args ...string) *Context {
	return &Context{
		Args: args,
		Cloud: func() (api.Cloud, error) {
			return cloud, nil
		},
	}
}

// profiles stands in for the configured profile names prod, local and staging.
func profiles(word string, _ *Context) ([]string, error) {
	var names []string

	for _, name := range []string{"prod", "local", "staging"} {
		if strings.HasPrefix(name, word) {
			names = append(names, name)
		}
	}

	return names, nil
}

// volumePaths stands in for the volume path completer: the scheme, then the
// account's volumes by name prefix.
func volumePaths(word string, ctx *Context) ([]string, error) {
	if !strings.HasPrefix(word, Scheme) {
		isPartialScheme := strings.HasPrefix(word, "vol") && strings.HasPrefix(Scheme, word)

		if isPartialScheme {
			return []string{Scheme}, nil
		}

		return nil, nil
	}

	cloud, err := ctx.Cloud()
	if err != nil {
		return nil, err
	}

	volumes, err := cloud.ListVolumes(context.Background())
	if err != nil {
		return nil, err
	}

	name := strings.TrimPrefix(word, Scheme)

	var paths []string

	for _, volume := range volumes {
		if strings.HasPrefix(volume.Name, name) {
			paths = append(paths, Scheme+volume.Name+"/")
		}
	}

	return paths, nil
}

func volumeMounts(word string, _ *Context) ([]string, error) {
	return []string{word + ":"}, nil
}

// newCommand is a runnable command that keeps its flags in definition order.
func newCommand(use string) *cobra.Command {
	cmd := &cobra.Command{
		Use: use,
		Run: func(*cobra.Command, []string) {},
	}

	cmd.Flags().SortFlags = false

	return cmd
}

func testProgram() *cobra.Command {
	program := newCommand("featherless-cloud")

	program.PersistentFlags().Bool("json", false, "output as JSON")
	program.PersistentFlags().String("profile", "", "profile to use")
	Flag(program, "profile", profiles)

	volume := newCommand("volume")

	ls := newCommand("ls")
	Args(ls, Arg{Name: "path", Complete: volumePaths})
	ls.Flags().String("profile", "", "profile")
	Flag(ls, "profile", profiles)

	rm := newCommand("rm")
	Args(rm, Arg{Name: "paths", Variadic: true, Complete: volumePaths})
	rm.Flags().Bool("yes", false, "skip the confirmation prompt")

	volume.AddCommand(ls, rm)

	execCommand := newCommand("exec")
	execCommand.Flags().String("name", "", "Run Job name")

	get := newCommand("get")
	Args(get, Arg{Name: "id"})

	completionCommand := newCommand("completion")
	Args(completionCommand, Arg{Name: "shell", Choices: []string{"bash", "zsh"}})

	program.AddCommand(volume, execCommand, get, completionCommand)

	return program
}

func testTree() *Node {
	return CommandTree(testProgram())
}

func findCommand(node *Node, name string) *Node {
	if node == nil {
		return nil
	}

	for _, command := range node.Commands {
		if command.Name == name {
			return command
		}
	}

	return nil
}

func findOption(node *Node, long string) *Option {
	if node == nil {
		return nil
	}

	for index := range node.Options {
		if node.Options[index].Long == long {
			return &node.Options[index]
		}
	}

	return nil
}

func commandNames(node *Node) []string {
	if node == nil {
		return nil
	}

	var names []string

	for _, command := range node.Commands {
		names = append(names, command.Name)
	}

	return names
}

func sameCompleter(a Completer, b Completer) bool {
	if a == nil || b == nil {
		return false
	}

	return reflect.ValueOf(a).Pointer() == reflect.ValueOf(b).Pointer()
}

func expectStrings(t *testing.T, label string, got []string, want []string) {
	t.Helper()

	if !slices.Equal(got, want) {
		t.Errorf("%s = %q, want %q", label, got, want)
	}
}

func TestDeclareKeepsFlagForms(t *testing.T) {
	newProgram := func() *cobra.Command {
		cmd := newCommand("x")

		cmd.Flags().String("tail", "500", "lines")
		cmd.Flags().StringArray("env", nil, "env")
		cmd.Flags().StringSlice("tag", []string{"a"}, "tags")
		Flag(cmd, "env", volumeMounts)
		Flag(cmd, "tag", profiles)

		return cmd
	}

	defaults := newProgram()

	if err := defaults.ParseFlags([]string{}); err != nil {
		t.Fatalf("parse: %v", err)
	}

	tail, _ := defaults.Flags().GetString("tail")
	env, _ := defaults.Flags().GetStringArray("env")
	tag, _ := defaults.Flags().GetStringSlice("tag")

	if tail != "500" {
		t.Errorf("tail = %q, want %q", tail, "500")
	}

	expectStrings(t, "env", env, []string{})
	expectStrings(t, "tag", tag, []string{"a"})

	repeated := newProgram()

	if err := repeated.ParseFlags([]string{"--env", "A=1", "--env", "B=2"}); err != nil {
		t.Fatalf("parse: %v", err)
	}

	env, _ = repeated.Flags().GetStringArray("env")
	expectStrings(t, "env", env, []string{"A=1", "B=2"})
}

func TestDeclareAppliesDefaultsParsingAndChoices(t *testing.T) {
	newProgram := func() *cobra.Command {
		cmd := newCommand("x")

		Args(cmd, Arg{Name: "path", Complete: volumePaths})
		cmd.Flags().StringArray("volume", nil, "mount")
		Flag(cmd, "volume", volumeMounts)
		cmd.Flags().String("compute-type", "gpu", "type")
		Choices(cmd, "compute-type", "gpu", "cpu")

		return cmd
	}

	defaults := newProgram()

	if err := defaults.ParseFlags([]string{}); err != nil {
		t.Fatalf("parse: %v", err)
	}

	volume, _ := defaults.Flags().GetStringArray("volume")
	computeType, _ := defaults.Flags().GetString("compute-type")

	expectStrings(t, "volume", volume, []string{})

	if computeType != "gpu" {
		t.Errorf("compute-type = %q, want %q", computeType, "gpu")
	}

	mounts := newProgram()

	if err := mounts.ParseFlags([]string{"--volume", "a:/x", "--volume", "b:/y"}); err != nil {
		t.Fatalf("parse: %v", err)
	}

	volume, _ = mounts.Flags().GetStringArray("volume")
	expectStrings(t, "volume", volume, []string{"a:/x", "b:/y"})

	allowed := newProgram()

	if err := allowed.ParseFlags([]string{"--compute-type", "cpu"}); err != nil {
		t.Fatalf("parse: %v", err)
	}

	computeType, _ = allowed.Flags().GetString("compute-type")

	if computeType != "cpu" {
		t.Errorf("compute-type = %q, want %q", computeType, "cpu")
	}

	err := newProgram().ParseFlags([]string{"--compute-type", "tpu"})

	if err == nil || !strings.Contains(err.Error(), "Allowed choices are gpu, cpu") {
		t.Errorf("parse --compute-type tpu: err = %v, want Allowed choices are gpu, cpu", err)
	}
}

func TestDeclareRejectsArgumentsOutsideTheirChoicesAndKeepsTheCommandsOwnCheck(t *testing.T) {
	cmd := newCommand("completion")

	cmd.Args = cobra.ExactArgs(1)
	Args(cmd, Arg{Name: "shell", Choices: []string{"bash", "zsh"}})

	if err := cmd.ValidateArgs([]string{"bash"}); err != nil {
		t.Errorf("bash: err = %v, want none", err)
	}

	rejected := cmd.ValidateArgs([]string{"fish"})
	want := "command-argument value 'fish' is invalid for argument 'shell'. Allowed choices are bash, zsh."

	if rejected == nil || rejected.Error() != want {
		t.Errorf("fish: err = %v, want %q", rejected, want)
	}

	if err := cmd.ValidateArgs([]string{}); err == nil {
		t.Error("no argument: err = nil, want the command's own ExactArgs error")
	}
}

func TestDeclareRegistersEachCompleterOnTheFlagOrArgumentItDefines(t *testing.T) {
	program := newCommand("x")

	Args(program, Arg{Name: "path", Complete: volumePaths})
	program.Flags().StringArray("volume", nil, "mount")
	Flag(program, "volume", volumeMounts)
	program.Flags().String("tail", "500", "lines")

	tree := CommandTree(program)

	if len(tree.Arguments) == 0 || !sameCompleter(tree.Arguments[0].Complete, volumePaths) {
		t.Errorf("argument completer is not volumePaths: %+v", tree.Arguments)
	}

	volume := findOption(tree, "--volume")

	if volume == nil || !sameCompleter(volume.Complete, volumeMounts) {
		t.Errorf("--volume completer is not volumeMounts: %+v", volume)
	}

	tail := findOption(tree, "--tail")

	if tail == nil || tail.Complete != nil {
		t.Errorf("--tail = %+v, want an option without a completer", tail)
	}
}

func TestDeclareRegistersCompletersOnSubcommands(t *testing.T) {
	program := newCommand("x")
	volume := newCommand("volume")
	ls := newCommand("ls")

	Args(ls, Arg{Name: "path", Complete: volumePaths})
	ls.Flags().String("profile", "", "profile")
	Flag(ls, "profile", profiles)
	volume.AddCommand(ls)
	program.AddCommand(volume)

	node := findCommand(findCommand(CommandTree(program), "volume"), "ls")

	if node == nil {
		t.Fatal("volume ls is missing from the tree")
	}

	if len(node.Arguments) == 0 || !sameCompleter(node.Arguments[0].Complete, volumePaths) {
		t.Errorf("argument completer is not volumePaths: %+v", node.Arguments)
	}

	profile := findOption(node, "--profile")

	if profile == nil || !sameCompleter(profile.Complete, profiles) {
		t.Errorf("--profile completer is not profiles: %+v", profile)
	}
}

func TestCommandTreeIncludesVisibleCommandsWithAliasesAndGlobalOptionsExcludesHiddenOnes(t *testing.T) {
	program := newCommand("featherless-cloud")

	program.PersistentFlags().Bool("json", false, "json output")

	runjob := newCommand("runjob")
	runjob.Aliases = []string{"job"}
	runjob.AddCommand(newCommand("list"))

	checkUpdate := newCommand("_check-update")
	checkUpdate.Hidden = true

	program.AddCommand(runjob, checkUpdate)

	tree := CommandTree(program)

	expectStrings(t, "commands", commandNames(tree), []string{"runjob", "job", "help"})
	expectStrings(t, "runjob commands", commandNames(findCommand(tree, "runjob")), []string{"list", "help"})

	want := []Option{{Long: "--json"}, {Long: "--help"}}

	if !reflect.DeepEqual(tree.Options, want) {
		t.Errorf("options = %+v, want %+v", tree.Options, want)
	}
}

func TestCommandTreeGivesHelpNodesTheParentCommandToList(t *testing.T) {
	help := findCommand(testTree(), "help")

	if help == nil || help.HelpFor == nil {
		t.Fatalf("help node = %+v, want one listing the program's commands", help)
	}

	if !slices.Contains(commandNames(help.HelpFor), "volume") {
		t.Errorf("help lists %q, want volume among them", commandNames(help.HelpFor))
	}
}

func TestTokenizeLineDecodesEscapesAndQuotes(t *testing.T) {
	cases := []struct {
		line  string
		words []string
		open  bool
	}{
		{`featherless-cloud volume ls vol://my\ data/x`, []string{"featherless-cloud", "volume", "ls", "vol://my data/x"}, true},
		{`featherless-cloud volume ls "vol://my da`, []string{"featherless-cloud", "volume", "ls", "vol://my da"}, true},
		{`featherless-cloud volume ls `, []string{"featherless-cloud", "volume", "ls"}, false},
	}

	for _, test := range cases {
		words, open := TokenizeLine(test.line)

		expectStrings(t, test.line, words, test.words)

		if open != test.open {
			t.Errorf("%s: open = %v, want %v", test.line, open, test.open)
		}
	}
}

func TestWalkCommandTreeResolvesTheDeepestCommandOptionsAndPositionalValues(t *testing.T) {
	tree := testTree()

	ls := WalkCommandTree(tree, []string{"volume", "ls"})

	if ls.Node == nil || ls.Node.Name != "ls" {
		t.Errorf("volume ls resolves to %+v, want ls", ls.Node)
	}

	answered := WalkCommandTree(tree, []string{"volume", "ls", "--profile", "prod"})

	if answered.AfterFlag != nil {
		t.Errorf("after --profile prod: AfterFlag = %+v, want none", answered.AfterFlag)
	}

	pending := WalkCommandTree(tree, []string{"volume", "ls", "--profile"})

	if pending.AfterFlag == nil || pending.AfterFlag.Long != "--profile" {
		t.Errorf("after --profile: AfterFlag = %+v, want --profile", pending.AfterFlag)
	}

	rm := WalkCommandTree(tree, []string{"volume", "rm", "vol://x/", "--yes", "vol://y/"})

	expectStrings(t, "args", rm.Args, []string{"vol://x/", "vol://y/"})
}

func TestWalkCommandTreeWalksAliases(t *testing.T) {
	program := newCommand("featherless-cloud")
	runjob := newCommand("runjob")

	runjob.Aliases = []string{"job"}
	runjob.AddCommand(newCommand("list"))
	program.AddCommand(runjob)

	position := WalkCommandTree(CommandTree(program), []string{"job", "list"})

	if position.Node == nil || position.Node.Name != "list" {
		t.Errorf("job list resolves to %+v, want list", position.Node)
	}
}

// complete completes word after the words following the program name in testTree.
func complete(t *testing.T, word string, words []string, ctx *Context) []string {
	t.Helper()

	position := WalkCommandTree(testTree(), words)
	candidates, err := CompleteWord(word, position, ctx)

	if err != nil {
		t.Fatalf("complete %q after %q: %v", word, words, err)
	}

	return candidates
}

func TestCompleteWordCompletesSubcommandsAfterTheProgramName(t *testing.T) {
	expectStrings(t, "vol", complete(t, "vol", nil, completerContext(&fakeCloud{})), []string{"volume"})
	expectStrings(t, "vo", complete(t, "vo", nil, completerContext(&fakeCloud{})), []string{"volume"})
}

func TestCompleteWordCompletesFlagsAtAnyPosition(t *testing.T) {
	words := []string{"volume", "rm"}

	expectStrings(t, "--", complete(t, "--", words, completerContext(&fakeCloud{})), []string{"--json", "--profile", "--help", "--yes"})
	expectStrings(t, "--pro", complete(t, "--pro", words, completerContext(&fakeCloud{})), []string{"--profile"})
}

func TestCompleteWordCompletesAnOptionValueWithItsCompleterAfterTheFlagAndAfterEquals(t *testing.T) {
	afterFlag := []string{"volume", "ls", "--profile"}

	expectStrings(t, "p", complete(t, "p", afterFlag, completerContext(&fakeCloud{})), []string{"prod"})
	expectStrings(t, "''", complete(t, "", afterFlag, completerContext(&fakeCloud{})), []string{"prod", "local", "staging"})
	expectStrings(t, "--profile=p", complete(t, "--profile=p", []string{"volume", "ls"}, completerContext(&fakeCloud{})), []string{"--profile=prod"})
}

func TestCompleteWordCompletesNothingForAnOptionWithoutACompleter(t *testing.T) {
	ctx := completerContext(&fakeCloud{})
	cloud := ctx.Cloud
	calls := 0

	ctx.Cloud = func() (api.Cloud, error) {
		calls++
		return cloud()
	}

	expectStrings(t, "--name", complete(t, "", []string{"exec", "--name"}, ctx), []string{})
	expectStrings(t, "--name=x", complete(t, "--name=x", []string{"exec"}, ctx), []string{})

	if calls != 0 {
		t.Errorf("the client was created %d times, want none", calls)
	}
}

func TestCompleteWordCompletesAnArgumentWithItsCompleterAfterAFlagThatTakesNoValue(t *testing.T) {
	candidates := complete(t, "vol://da", []string{"volume", "rm", "--yes"}, completerContext(withVolumes(datasets)))

	expectStrings(t, "vol://da", candidates, []string{"vol://datasets/"})
}

func TestCompleteWordCompletesEveryPositionOfAVariadicArgument(t *testing.T) {
	candidates := complete(t, "vol://da", []string{"volume", "rm", "vol://models/x"}, completerContext(withVolumes(datasets)))

	expectStrings(t, "vol://da", candidates, []string{"vol://datasets/"})
}

func TestCompleteWordCompletesNothingPastTheDeclaredArgumentsOrForAnUndeclaredOne(t *testing.T) {
	ctx := completerContext(withVolumes(datasets))

	expectStrings(t, "volume ls", complete(t, "vol://da", []string{"volume", "ls", "vol://models/"}, ctx), []string{})
	expectStrings(t, "get", complete(t, "vol://da", []string{"get"}, ctx), []string{})
}

func TestCompleteWordCompletesChoices(t *testing.T) {
	expectStrings(t, "b", complete(t, "b", []string{"completion"}, completerContext(&fakeCloud{})), []string{"bash"})
	expectStrings(t, "''", complete(t, "", []string{"completion"}, completerContext(&fakeCloud{})), []string{"bash", "zsh"})
}

func TestCompleteWordListsCommandsAfterHelp(t *testing.T) {
	expectStrings(t, "v", complete(t, "v", []string{"help"}, completerContext(&fakeCloud{})), []string{"volume"})
}

func TestCompleteWordFallsThroughToTheArgumentWhenNoSubcommandMatches(t *testing.T) {
	expectStrings(t, "vol", complete(t, "vol", []string{"volume", "ls"}, completerContext(&fakeCloud{})), []string{"vol://"})
}

func TestProfileFromCommandFindsTheLastProfileValue(t *testing.T) {
	cases := map[string]string{
		"featherless-cloud --profile prod volume ls vol://x":              "prod",
		"featherless-cloud --profile=prod volume ls vol://x":              "prod",
		"featherless-cloud --profile dev --profile prod volume ls vol://": "prod",
		"featherless-cloud volume ls vol://x":                             "",
	}

	for line, want := range cases {
		if got := ProfileFromCommand(line); got != want {
			t.Errorf("ProfileFromCommand(%q) = %q, want %q", line, got, want)
		}
	}
}

func TestRunCompletesTheLineUpToTheCursorForItsProfile(t *testing.T) {
	line := "featherless-cloud --profile prod volume rm vol://da"
	env := map[string]string{
		"COMP_LINE":  line + " vol://models/",
		"COMP_POINT": strconv.Itoa(len(line)),
		"FC_WORD":    "vol://da",
	}

	var requested []string

	newCloud := func(profile string) (api.Cloud, error) {
		requested = append(requested, profile)
		return withVolumes(datasets), nil
	}

	var out bytes.Buffer

	Run(testProgram(), func(name string) string { return env[name] }, newCloud, &out)

	if out.String() != "vol://datasets/\n" {
		t.Errorf("output = %q, want %q", out.String(), "vol://datasets/\n")
	}

	expectStrings(t, "profiles", requested, []string{"prod"})
}

func TestRunListsHelpOnceBesideCobrasHelpCommand(t *testing.T) {
	program := testProgram()

	// Execute adds cobra's own help command before _complete runs.
	program.InitDefaultHelpCmd()

	env := map[string]string{"COMP_LINE": "featherless-cloud he", "FC_WORD": "he"}

	newCloud := func(string) (api.Cloud, error) {
		return withVolumes(), nil
	}

	var out bytes.Buffer

	Run(program, func(name string) string { return env[name] }, newCloud, &out)

	if out.String() != "help\n" {
		t.Errorf("output = %q, want %q", out.String(), "help\n")
	}
}

func TestRunPrintsNothingWhenACompleterFails(t *testing.T) {
	env := map[string]string{"COMP_LINE": "featherless-cloud volume ls vol://da", "FC_WORD": "vol://da"}

	newCloud := func(string) (api.Cloud, error) {
		return nil, errors.New("not signed in")
	}

	var out bytes.Buffer

	Run(testProgram(), func(name string) string { return env[name] }, newCloud, &out)

	if out.Len() != 0 {
		t.Errorf("output = %q, want nothing", out.String())
	}
}

func nonEmptyLines(text string) []string {
	var lines []string

	for _, line := range strings.Split(text, "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}

	return lines
}

func runShell(t *testing.T, cmd *exec.Cmd) string {
	t.Helper()

	output, err := cmd.Output()

	if err != nil {
		var exitErr *exec.ExitError

		if errors.As(err, &exitErr) {
			t.Fatalf("%s: %v\n%s", cmd.Path, err, exitErr.Stderr)
		}

		t.Fatalf("%s: %v", cmd.Path, err)
	}

	return string(output)
}

const bashHarness = `featherless-cloud() { printf "%s\n" "$STUB"; }
compopt() { printf "%s\n" "$*" >> "$RECORDED"; }
COMP_WORDBREAKS=$WORDBREAKS COMP_LINE=$LINE COMP_POINT=${#LINE}
_featherless_cloud_completions
printf "%s\n" "${COMPREPLY[@]}"`

// bashComplete runs the bash completion function as readline does, with the
// CLI stubbed to print candidates and compopt recorded.
func bashComplete(t *testing.T, line string, candidates []string) (reply []string, compopt []string) {
	t.Helper()

	recorded := filepath.Join(t.TempDir(), "compopt")
	harness := BashScript() + "\n" + bashHarness
	cmd := exec.Command("bash", "--norc", "--noprofile", "-c", harness)

	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"LINE=" + line,
		"STUB=" + strings.Join(candidates, "\n"),
		"WORDBREAKS= \t\n\"'><=;|&(:",
		"RECORDED=" + recorded,
		"COLUMNS=40",
	}

	output := runShell(t, cmd)
	calls, err := os.ReadFile(recorded)

	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read compopt calls: %v", err)
	}

	return nonEmptyLines(output), nonEmptyLines(string(calls))
}

func TestBashScriptReplacesOnlyThePartOfTheWordAfterTheLastColonOrEqualsReadlineBreaksOn(t *testing.T) {
	reply, _ := bashComplete(t, "featherless-cloud volume ls vol://da", []string{"vol://datasets/"})
	expectStrings(t, "vol://da", reply, []string{"//datasets/"})

	reply, _ = bashComplete(t, `featherless-cloud volume ls vol://datasets/my\ data/re`, []string{"vol://datasets/my data/report 2024.txt"})
	expectStrings(t, "vol://datasets/my\\ data/re", reply, []string{"//datasets/my data/report 2024.txt"})

	reply, _ = bashComplete(t, "featherless-cloud volume ls --profile=p", []string{"--profile=prod"})
	expectStrings(t, "--profile=p", reply, []string{"prod"})

	reply, _ = bashComplete(t, "featherless-cloud vol", []string{"volume"})
	expectStrings(t, "vol", reply, []string{"volume"})
}

func TestBashScriptKeepsTheWholeWordWhenTheColonIsEscapedOrInsideAnOpenQuote(t *testing.T) {
	reply, _ := bashComplete(t, `featherless-cloud volume ls vol\://da`, []string{"vol://datasets/"})
	expectStrings(t, `vol\://da`, reply, []string{"vol://datasets/"})

	reply, _ = bashComplete(t, `featherless-cloud volume ls "vol://datasets/my d`, []string{"vol://datasets/my data/"})
	expectStrings(t, `"vol://datasets/my d`, reply, []string{"vol://datasets/my data/"})

	reply, _ = bashComplete(t, `featherless-cloud volume ls vol:"//datasets/my d`, []string{"vol://datasets/my data/"})
	expectStrings(t, `vol:"//datasets/my d`, reply, []string{"//datasets/my data/"})
}

func TestBashScriptPadsSeveralNamesPastHalfTheScreenSoReadlineListsOnePerRow(t *testing.T) {
	var padded []string

	for _, name := range []string{"shell", "ssh", "start"} {
		padded = append(padded, fmt.Sprintf("%-20s", name))
	}

	reply, _ := bashComplete(t, "featherless-cloud instance s", []string{"shell", "ssh", "start"})
	expectStrings(t, "instance s", reply, padded)

	reply, _ = bashComplete(t, "featherless-cloud instance sh", []string{"shell"})
	expectStrings(t, "instance sh", reply, []string{"shell"})

	reply, _ = bashComplete(t, "featherless-cloud volume ls vol://datasets/", []string{"vol://datasets/a", "vol://datasets/b/"})
	expectStrings(t, "vol://datasets/", reply, []string{"//datasets/a", "//datasets/b/"})
}

func TestBashScriptAddsNoSpaceAfterAFolderOrANameEndingInColon(t *testing.T) {
	_, compopt := bashComplete(t, "featherless-cloud volume ls vol://da", []string{"vol://datasets/"})
	expectStrings(t, "vol://da", compopt, []string{"-o nospace"})

	_, compopt = bashComplete(t, "featherless-cloud instance create --volume da", []string{"datasets:"})
	expectStrings(t, "--volume da", compopt, []string{"-o nospace"})

	_, compopt = bashComplete(t, "featherless-cloud vol", []string{"volume"})
	expectStrings(t, "vol", compopt, []string{})
}

const zshHarness = `compadd() {
  local -a call
  while (( $# )); do
    if [[ $1 == -d ]]; then call+=(-d "${(j:,:)${(@P)2}}"); shift 2; else call+=("$1"); shift; fi
  done
  print -r -- "compadd ${(j: :)${(@qqq)call}}"
}
_files() { print -r -- _files; }
compdef() { print -r -- "compdef $*"; }
featherless-cloud() { print -r -- "$COMP_LINE|$FC_WORD" >| $DIR/cli; print -r -- "$STUB"; }
fpath=($DIR $fpath)
autoload -Uz _featherless-cloud
words=("${(@f)WORDS}") CURRENT=${#${(@f)WORDS}} PREFIX=${${(@f)WORDS}[-1]}
BUFFER=${(j: :)words} CURSOR=${#${(j: :)words}}
_featherless-cloud`

// zshComplete autoloads the zsh completion function like compinit and runs it
// once, with compadd, _files and the CLI stubbed.
func zshComplete(t *testing.T, words []string, candidates []string) (calls []string, cli string) {
	t.Helper()

	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "_featherless-cloud"), []byte(ZshScript()), 0o644); err != nil {
		t.Fatalf("write completion function: %v", err)
	}

	cmd := exec.Command("zsh", "-f", "-c", zshHarness)

	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"DIR=" + dir,
		"WORDS=" + strings.Join(words, "\n"),
		"STUB=" + strings.Join(candidates, "\n"),
	}

	output := runShell(t, cmd)
	recorded, err := os.ReadFile(filepath.Join(dir, "cli"))

	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read CLI call: %v", err)
	}

	return nonEmptyLines(output), strings.TrimRight(string(recorded), " \t\n")
}

func TestZshScriptCompletesOnTheFirstTabAfterCompinitAutoloadsIt(t *testing.T) {
	calls, cli := zshComplete(t, []string{"featherless-cloud", "vol"}, []string{"volume"})

	expectStrings(t, "calls", calls, []string{`compadd "-l" "-d" "volume" "--" "volume"`})

	if cli != "featherless-cloud |vol" {
		t.Errorf("cli = %q, want %q", cli, "featherless-cloud |vol")
	}
}

func TestZshScriptPassesTheWordUnquotedAndShowsOnlyTheLastPathComponent(t *testing.T) {
	calls, cli := zshComplete(t, []string{"featherless-cloud", "volume", "ls", `vol://datasets/my\ d`}, []string{"vol://datasets/my data/", "vol://datasets/my data.txt"})

	expectStrings(t, "calls", calls, []string{
		`compadd "-l" "-S" "" "-d" "my data/" "--" "vol://datasets/my data/"`,
		`compadd "-l" "-d" "my data.txt" "--" "vol://datasets/my data.txt"`,
	})

	if cli != "featherless-cloud volume ls |vol://datasets/my d" {
		t.Errorf("cli = %q, want %q", cli, "featherless-cloud volume ls |vol://datasets/my d")
	}

	calls, _ = zshComplete(t, []string{"featherless-cloud", "volume", "ls", "vol://"}, []string{"vol://datasets/"})
	expectStrings(t, "vol://", calls, []string{`compadd "-l" "-S" "" "-d" "datasets/" "--" "vol://datasets/"`})

	calls, _ = zshComplete(t, []string{"featherless-cloud", "volume", "ls", "vol"}, []string{"vol://"})
	expectStrings(t, "vol", calls, []string{`compadd "-l" "-S" "" "-d" "vol://" "--" "vol://"`})
}

func TestZshScriptAddsNoSpaceAfterANameEndingInColon(t *testing.T) {
	calls, _ := zshComplete(t, []string{"featherless-cloud", "instance", "create", "--volume", "da"}, []string{"datasets:"})

	expectStrings(t, "calls", calls, []string{`compadd "-l" "-S" "" "-d" "datasets:" "--" "datasets:"`})
}

func TestZshScriptFallsBackToLocalFilesWhenTheCLIHasNothing(t *testing.T) {
	calls, _ := zshComplete(t, []string{"featherless-cloud", "volume", "cp", "./we"}, []string{})

	expectStrings(t, "calls", calls, []string{"_files"})
}

func TestZshScriptRegistersItselfWithCompdefWhenSourced(t *testing.T) {
	cmd := exec.Command("zsh", "-f", "-c", "compdef() { print -r -- \"compdef $*\"; }\neval \"$SCRIPT\"")

	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"SCRIPT=" + ZshScript(),
	}

	output := strings.TrimSpace(runShell(t, cmd))

	if output != "compdef _featherless-cloud featherless-cloud" {
		t.Errorf("output = %q, want %q", output, "compdef _featherless-cloud featherless-cloud")
	}
}
