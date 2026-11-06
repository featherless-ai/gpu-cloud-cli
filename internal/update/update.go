// Package update offers newer releases at startup and keeps the release check
// fresh in the background.
package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"golang.org/x/term"

	"featherless.ai/featherless-cloud/internal/config"
	"featherless.ai/featherless-cloud/internal/errs"
	"featherless.ai/featherless-cloud/internal/output"
	"featherless.ai/featherless-cloud/internal/version"
)

const (
	stateFileName = "featherless-cloud-update-check.json"
	ttl           = 24 * time.Hour
	fetchTimeout  = 5 * time.Second
)

// Commands that run without a person reading the output, or are about versions themselves.
var noPromptCommands = []string{"_check-update", "_complete", "completion", "upgrade"}

type updateState struct {
	Latest    string `json:"latest"`
	CheckedAt int64  `json:"checkedAt"`
	// Declined is a version the person said no to; they are asked again only about a newer one.
	Declined string `json:"declined,omitempty"`
}

// InstallMethod is how the running binary was installed, which decides how it upgrades.
type InstallMethod string

const (
	// InstallNpmGlobal is this package inside npm's global folder, the only install npm can update in place.
	InstallNpmGlobal InstallMethod = "npm-global"
	// InstallOther is any install the CLI cannot update in place.
	InstallOther InstallMethod = "other"
)

// Seams tests replace.
var (
	registryBase               = "https://registry.npmjs.org"
	stderr           io.Writer = os.Stderr
	stderrIsTerminal           = func() bool { return term.IsTerminal(int(os.Stderr.Fd())) }
	stdinIsTerminal            = func() bool { return term.IsTerminal(int(os.Stdin.Fd())) }
	prompter                   = output.New(output.Interactive).Prompt
	currentInstall             = CurrentInstall
	// startBackgroundCheck re-runs this binary as `_check-update`, detached, without waiting.
	startBackgroundCheck = startDetachedCheck
)

func stateFile() string {
	return filepath.Join(config.Dir(), stateFileName)
}

func readState() *updateState {
	raw, err := os.ReadFile(stateFile())
	if err != nil {
		return nil
	}

	var state updateState
	if err := json.Unmarshal(raw, &state); err != nil {
		return nil
	}

	return &state
}

// writeState is best-effort; a failed write just means the next run checks again.
func writeState(state updateState) {
	if err := os.MkdirAll(config.Dir(), 0o755); err != nil {
		return
	}

	content := output.EncodeJSON(state, false) + "\n"
	_ = os.WriteFile(stateFile(), []byte(content), 0o644)
}

func shouldSkip(argv []string) bool {
	if os.Getenv("CI") != "" {
		return true
	}

	if !stderrIsTerminal() {
		return true
	}

	command := ""

	for _, arg := range argv {
		if !strings.HasPrefix(arg, "-") {
			command = arg
			break
		}
	}

	isScripted := slices.Contains(argv, "--json") || slices.Contains(argv, "--non-interactive")
	return isScripted || slices.Contains(noPromptCommands, command)
}

// OfferUpdate offers a newer version the last background check found, then
// refreshes that check when it is a day old. An install the CLI can update in
// place, at a terminal, is asked whether to update now; anything else gets a
// one-line notice. Never touches the network itself. Call once at startup
// with os.Args[1:].
func OfferUpdate(argv []string) {
	if shouldSkip(argv) {
		return
	}

	state := readState()
	shouldOffer := state != nil && version.IsNewer(state.Latest, version.Version) && state.Declined != state.Latest

	if shouldOffer {
		canUpdateInPlace := stdinIsTerminal() && currentInstall() == InstallNpmGlobal

		if canUpdateInPlace {
			askToUpdate(*state)
		} else {
			fmt.Fprintf(stderr, "\nUpdate available: %s → %s. Run `featherless-cloud upgrade`.\n\n", version.Version, state.Latest)
		}
	}

	if state == nil || isStale(*state) {
		scheduleBackgroundCheck(readState())
	}
}

func isStale(state updateState) bool {
	age := time.Now().UnixMilli() - state.CheckedAt
	return age > ttl.Milliseconds()
}

func askToUpdate(state updateState) {
	message := fmt.Sprintf("featherless-cloud %s is available (you have %s). Update now?", state.Latest, version.Version)
	shouldUpdate, err := prompter.Confirm(message, true)
	isCancelled := errors.Is(err, output.ErrCancelled)

	if err != nil && !isCancelled {
		return
	}

	if isCancelled || !shouldUpdate {
		state.Declined = state.Latest
		writeState(state)
		fmt.Fprint(stderr, "Run `featherless-cloud upgrade` whenever you want it.\n\n")
		return
	}

	if err := Upgrade(InstallNpmGlobal, state.Latest, os.Stdout); err != nil {
		fmt.Fprintf(stderr, "\n%s\n\n", err.Error())
		return
	}

	fmt.Fprintf(stderr, "\nUpdated featherless-cloud to %s.\n\n", state.Latest)
}

func scheduleBackgroundCheck(previous *updateState) {
	// Stamp checkedAt now (keeping what is known) so rapid successive runs
	// don't each start a check before the first one writes its result.
	stamped := updateState{}

	if previous != nil {
		stamped = *previous
	}

	if stamped.Latest == "" {
		stamped.Latest = version.Version
	}

	stamped.CheckedAt = time.Now().UnixMilli()
	writeState(stamped)

	_ = startBackgroundCheck()
}

func startDetachedCheck() error {
	executable, err := os.Executable()
	if err != nil {
		return err
	}

	child := exec.Command(executable, "_check-update")
	child.SysProcAttr = detachedProcess()

	if err := child.Start(); err != nil {
		return err
	}

	return child.Process.Release()
}

// RunUpdateCheck runs inside the detached `_check-update` child and refreshes
// the cached latest version. Failures are swallowed; the next stale run retries.
func RunUpdateCheck(ctx context.Context) {
	latest, err := FetchLatestVersion(ctx)
	if err != nil {
		return
	}

	state := updateState{}

	if previous := readState(); previous != nil {
		state = *previous
	}

	state.Latest = latest
	state.CheckedAt = time.Now().UnixMilli()
	writeState(state)
}

// FetchLatestVersion asks the npm registry for the latest published version.
func FetchLatestVersion(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()

	url := registryBase + "/" + version.PackageName + "/latest"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()

	isOK := response.StatusCode >= 200 && response.StatusCode < 300
	if !isOK {
		return "", errs.Newf("Release lookup failed with status %d.", response.StatusCode)
	}

	var parsed struct {
		Version any `json:"version"`
	}

	malformed := errs.New("Release index is malformed.")

	if err := json.NewDecoder(response.Body).Decode(&parsed); err != nil {
		return "", malformed
	}

	latest, isString := parsed.Version.(string)
	if !isString {
		return "", malformed
	}

	return latest, nil
}

// Upgrade installs release the way method updates. The installer's own output
// goes to stdout; pass stderr in --json mode so stdout stays one JSON document.
// Every method upgrades through npm until the others get their own installer.
func Upgrade(method InstallMethod, release string, stdout io.Writer) error {
	spec := version.PackageName + "@" + release
	return npmInstall(spec, stdout)
}

func npmInstall(spec string, stdout io.Writer) error {
	child := exec.Command("npm", "install", "--global", "--prefer-online", spec)
	child.Stdout = stdout
	child.Stderr = os.Stderr

	err := child.Run()
	if err == nil {
		return nil
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return errs.Newf("npm exited with code %d. Run: npm install --global %s", exitErr.ExitCode(), spec)
	}

	return errs.Newf("Could not run npm: %s. Run: npm install --global %s", err.Error(), spec)
}

// packagePath is the package's folder relative to a node_modules folder.
func packagePath() string {
	return filepath.Join(strings.Split(version.PackageName, "/")...)
}

// IsGlobalNpmInstall reports whether executable is this package inside npm's
// global folder globalRoot.
func IsGlobalNpmInstall(executable, globalRoot string) bool {
	packageDir := filepath.Join(globalRoot, packagePath()) + string(filepath.Separator)
	return strings.HasPrefix(executable, packageDir)
}

// DetectInstall tells how executable (symlinks resolved) was installed, given
// npm's global folders.
func DetectInstall(executable string, npmRoots []string) InstallMethod {
	for _, root := range npmRoots {
		if IsGlobalNpmInstall(executable, root) {
			return InstallNpmGlobal
		}
	}

	return InstallOther
}

// CurrentInstall detects how the running binary was installed.
func CurrentInstall() InstallMethod {
	executable, err := os.Executable()
	if err != nil {
		return InstallOther
	}

	resolved, err := filepath.EvalSymlinks(executable)
	if err != nil {
		return InstallOther
	}

	return DetectInstall(resolved, npmGlobalRoots(resolved))
}

// npmGlobalRoots works npm's global folder out from the binary's own path: the
// platform package sits inside <root>/@featherlessai/gpu-cloud-cli/node_modules,
// and root is <prefix>/lib/node_modules (<prefix>\node_modules on Windows). The
// prefix is confirmed by npm_config_prefix when set, otherwise by npm's link to
// the command in it, which npx caches and project installs lack.
func npmGlobalRoots(executable string) []string {
	separator := string(filepath.Separator)
	marker := separator + packagePath() + separator
	index := strings.Index(executable, marker)

	if index < 0 {
		return nil
	}

	root := executable[:index]

	if filepath.Base(root) != "node_modules" {
		return nil
	}

	prefix := filepath.Dir(root)
	link := filepath.Join(prefix, version.AppName+".cmd")

	if runtime.GOOS != "windows" {
		if filepath.Base(prefix) != "lib" {
			return nil
		}

		prefix = filepath.Dir(prefix)
		link = filepath.Join(prefix, "bin", version.AppName)
	}

	if configured := os.Getenv("npm_config_prefix"); configured != "" {
		if !isSamePath(prefix, configured) {
			return nil
		}

		return []string{root}
	}

	if _, err := os.Lstat(link); err != nil {
		return nil
	}

	return []string{root}
}

func isSamePath(a, b string) bool {
	resolvedA, err := filepath.EvalSymlinks(a)
	if err != nil {
		return false
	}

	resolvedB, err := filepath.EvalSymlinks(b)
	if err != nil {
		return false
	}

	if runtime.GOOS == "windows" {
		return strings.EqualFold(resolvedA, resolvedB)
	}

	return resolvedA == resolvedB
}
