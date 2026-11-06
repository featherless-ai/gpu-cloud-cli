// Package update offers newer releases at startup and keeps the release check
// fresh in the background.
package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
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
	brewUpgrade   = "brew upgrade featherless-cloud"
	installScript = "curl -fsSL https://console.cloud.featherless.ai/install.sh | sh"
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
	// InstallOther is a copy inside a package manager's node_modules that npm can't
	// update in place (npx caches, project installs); replacing it would desync that manager.
	InstallOther InstallMethod = "other"
	// InstallHomebrew is a binary in Homebrew's Cellar; brew upgrades it.
	InstallHomebrew InstallMethod = "homebrew"
	// InstallStandalone is a binary the installer or a person put in place; it replaces itself.
	InstallStandalone InstallMethod = "standalone"
)

// Seams tests replace.
var (
	registryBase               = "https://registry.npmjs.org"
	stderr           io.Writer = os.Stderr
	stderrIsTerminal           = func() bool { return term.IsTerminal(int(os.Stderr.Fd())) }
	stdinIsTerminal            = func() bool { return term.IsTerminal(int(os.Stdin.Fd())) }
	prompter                   = output.New(output.Interactive).Prompt
	currentInstall             = CurrentInstall
	executablePath             = runningExecutable
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
// refreshes that check when it is a day old. A global npm or standalone install
// at a terminal is asked whether to update now; anything else gets a one-line
// notice, pointing Homebrew installs at brew. Never touches the network itself.
// Call once at startup with os.Args[1:].
func OfferUpdate(argv []string) {
	if shouldSkip(argv) {
		return
	}

	state := readState()
	shouldOffer := state != nil && version.IsNewer(state.Latest, version.Version) && state.Declined != state.Latest

	if shouldOffer {
		method := currentInstall()
		canUpdateInPlace := method == InstallNpmGlobal || method == InstallStandalone
		shouldAsk := canUpdateInPlace && stdinIsTerminal()
		hint := "featherless-cloud upgrade"

		if method == InstallHomebrew {
			hint = brewUpgrade
		}

		if shouldAsk {
			askToUpdate(*state, method)
		} else {
			fmt.Fprintf(stderr, "\nUpdate available: %s → %s. Run `%s`.\n\n", version.Version, state.Latest, hint)
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

func askToUpdate(state updateState, method InstallMethod) {
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

	if err := Upgrade(context.Background(), method, state.Latest, os.Stdout); err != nil {
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

	body, err := registryGet(ctx, registryBase+"/"+version.PackageName+"/latest")
	if err != nil {
		return "", err
	}

	var parsed struct {
		Version any `json:"version"`
	}

	malformed := errs.New("Release index is malformed.")

	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", malformed
	}

	latest, isString := parsed.Version.(string)
	if !isString {
		return "", malformed
	}

	return latest, nil
}

func registryGet(ctx context.Context, url string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}

	unreachable := errs.New("Could not reach the npm registry.")

	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, unreachable
	}
	defer response.Body.Close()

	isOK := response.StatusCode >= 200 && response.StatusCode < 300
	if !isOK {
		return nil, errs.Newf("Release lookup failed with status %d.", response.StatusCode)
	}

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, unreachable
	}

	return body, nil
}

// Upgrade installs release the way method updates: brew for Homebrew, a
// self-replacing download for standalone, npm otherwise (brew picks its own
// version). Installer output goes to stdout; pass stderr in --json mode so
// stdout stays one JSON document.
func Upgrade(ctx context.Context, method InstallMethod, release string, stdout io.Writer) error {
	switch method {
	case InstallHomebrew:
		return runInstaller(stdout, brewUpgrade, "brew", "upgrade", version.AppName)
	case InstallStandalone:
		return replaceExecutable(ctx, release)
	default:
		spec := version.PackageName + "@" + release
		retry := "npm install --global " + spec
		return runInstaller(stdout, retry, "npm", "install", "--global", "--prefer-online", spec)
	}
}

// runInstaller runs a package manager; retry is the command to suggest when it fails.
func runInstaller(stdout io.Writer, retry string, name string, args ...string) error {
	child := exec.Command(name, args...)
	child.Stdout = stdout
	child.Stderr = os.Stderr

	err := child.Run()
	if err == nil {
		return nil
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		// Node reported a child a signal stopped as exiting with code null.
		code := "null"
		if exitErr.ExitCode() >= 0 {
			code = strconv.Itoa(exitErr.ExitCode())
		}

		return errs.Newf("%s exited with code %s. Run: %s", name, code, retry)
	}

	return errs.Newf("Could not run %s: %s. Run: %s", name, spawnError(name, err), retry)
}

// spawnError words a failed start as Node's spawn did, which the TypeScript CLI
// passed on: a name on PATH that isn't executable is EACCES, a missing one ENOENT.
func spawnError(name string, err error) string {
	if errors.Is(err, fs.ErrPermission) {
		return "spawn " + name + " EACCES"
	}

	if !errors.Is(err, exec.ErrNotFound) {
		return err.Error()
	}

	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		info, statErr := os.Stat(filepath.Join(dir, name))
		if statErr == nil && !info.IsDir() {
			return "spawn " + name + " EACCES"
		}
	}

	return "spawn " + name + " ENOENT"
}

// platformPackage is the npm package holding the binary for goos and goarch.
func platformPackage(goos, goarch string) (string, error) {
	osNames := map[string]string{"darwin": "darwin", "linux": "linux", "windows": "win32"}
	archNames := map[string]string{"arm64": "arm64", "amd64": "x64"}
	osName, hasOS := osNames[goos]
	archName, hasArch := archNames[goarch]

	if !hasOS || !hasArch {
		return "", errs.Newf("No featherless-cloud release for %s/%s.", goos, goarch)
	}

	return version.PackageName + "-" + osName + "-" + archName, nil
}

// replaceExecutable downloads release's binary for this platform and renames
// it over the running executable.
func replaceExecutable(ctx context.Context, release string) error {
	executable, err := executablePath()
	if err != nil {
		return errs.Newf("Can't find the running featherless-cloud: %s. Run: %s", err.Error(), installScript)
	}

	// Claim the file first so an unwritable folder fails before the download.
	staged := executable + ".new"

	if err := os.WriteFile(staged, nil, 0o755); err != nil {
		return cantReplace(executable, err)
	}
	defer os.Remove(staged)

	binary, err := downloadBinary(ctx, release)
	if err != nil {
		return err
	}

	if err := os.WriteFile(staged, binary, 0o755); err != nil {
		return cantReplace(executable, err)
	}

	if err := os.Chmod(staged, 0o755); err != nil {
		return cantReplace(executable, err)
	}

	if runtime.GOOS != "windows" {
		if err := os.Rename(staged, executable); err != nil {
			return cantReplace(executable, err)
		}

		return nil
	}

	// Windows can't overwrite a running executable, but it can rename it.
	aside := executable + ".old"
	_ = os.Remove(aside)

	if err := os.Rename(executable, aside); err != nil {
		return cantReplace(executable, err)
	}

	if err := os.Rename(staged, executable); err != nil {
		_ = os.Rename(aside, executable)
		return cantReplace(executable, err)
	}

	return nil
}

func cantReplace(executable string, err error) error {
	reason := err
	var pathErr *os.PathError
	var linkErr *os.LinkError

	if errors.As(err, &pathErr) {
		reason = pathErr.Err
	} else if errors.As(err, &linkErr) {
		reason = linkErr.Err
	}

	return errs.Newf("Can't replace %s: %s. Run: %s", executable, reason.Error(), installScript)
}

// downloadBinary fetches the platform package's tarball for release, checks it
// against the registry's sha512 integrity, and returns the binary inside.
func downloadBinary(ctx context.Context, release string) ([]byte, error) {
	name, err := platformPackage(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return nil, err
	}

	lookupCtx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()

	body, err := registryGet(lookupCtx, registryBase+"/"+name+"/"+release)
	if err != nil {
		return nil, err
	}

	var index struct {
		Dist struct {
			Tarball   string `json:"tarball"`
			Integrity string `json:"integrity"`
		} `json:"dist"`
	}

	malformed := errs.New("Release index is malformed.")

	if err := json.Unmarshal(body, &index); err != nil {
		return nil, malformed
	}

	// Integrity is a list of "<algorithm>-<base64 digest>" entries.
	expected := ""

	for _, entry := range strings.Fields(index.Dist.Integrity) {
		digest, isSHA512 := strings.CutPrefix(entry, "sha512-")

		if isSHA512 {
			expected = digest
			break
		}
	}

	if index.Dist.Tarball == "" || expected == "" {
		return nil, malformed
	}

	tarball, err := registryGet(ctx, index.Dist.Tarball)
	if err != nil {
		return nil, err
	}

	sum := sha512.Sum512(tarball)
	actual := base64.StdEncoding.EncodeToString(sum[:])

	if actual != expected {
		return nil, errs.Newf("Downloaded featherless-cloud %s failed its integrity check.", release)
	}

	invalid := errs.Newf("Downloaded featherless-cloud %s is not a valid release.", release)
	binaryName := "package/bin/" + version.AppName

	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}

	decompressed, err := gzip.NewReader(bytes.NewReader(tarball))
	if err != nil {
		return nil, invalid
	}

	archive := tar.NewReader(decompressed)

	for {
		header, err := archive.Next()
		if err != nil {
			return nil, invalid
		}

		isBinary := header.Name == binaryName && header.Typeflag == tar.TypeReg
		if !isBinary {
			continue
		}

		binary, err := io.ReadAll(archive)
		if err != nil {
			return nil, invalid
		}

		return binary, nil
	}
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

	separator := string(filepath.Separator)
	cellar := separator + "Cellar" + separator + version.AppName + separator

	if strings.Contains(executable, cellar) {
		return InstallHomebrew
	}

	if strings.Contains(executable, separator+"node_modules"+separator) {
		return InstallOther
	}

	return InstallStandalone
}

// CurrentInstall detects how the running binary was installed.
func CurrentInstall() InstallMethod {
	executable, err := executablePath()
	if err != nil {
		return InstallOther
	}

	return DetectInstall(executable, npmGlobalRoots(executable))
}

// runningExecutable is the running binary's path with symlinks resolved.
func runningExecutable() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}

	return filepath.EvalSymlinks(executable)
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
