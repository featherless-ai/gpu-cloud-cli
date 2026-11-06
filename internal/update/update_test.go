package update

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"featherless.ai/featherless-cloud/internal/output"
	"featherless.ai/featherless-cloud/internal/version"
)

func swap[T any](t *testing.T, target *T, value T) {
	t.Helper()
	previous := *target
	*target = value
	t.Cleanup(func() { *target = previous })
}

type declinePrompter struct{ output.Prompter }

func (declinePrompter) Confirm(message string, initial bool) (bool, error) {
	return false, output.ErrCancelled
}

// isolate gives the test its own HOME (the CLI derives its config dir from
// HOME), captures stderr, and stands in for the terminal and the spawned
// check. stdin is not a terminal and the install is not one npm can update,
// as under the test runner. Returns captured stderr and the state file path.
func isolate(t *testing.T, isStderrTerminal bool) (*bytes.Buffer, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CI", "")
	os.Unsetenv("CI")

	var captured bytes.Buffer
	swap(t, &stderr, io.Writer(&captured))
	swap(t, &stderrIsTerminal, func() bool { return isStderrTerminal })
	swap(t, &stdinIsTerminal, func() bool { return false })
	swap(t, &currentInstall, func() InstallMethod { return InstallOther })
	swap(t, &startBackgroundCheck, func() error { return nil })
	swap(t, &prompter, output.Prompter(declinePrompter{}))

	stateFile := filepath.Join(home, ".config", "featherless-cloud-update-check.json")
	return &captured, stateFile
}

// knownLatest writes a state whose check is fresh, so no background check is started.
func knownLatest(t *testing.T, stateFile, latest, declined string) {
	t.Helper()
	state := map[string]any{"latest": latest, "checkedAt": time.Now().UnixMilli()}

	if declined != "" {
		state["declined"] = declined
	}

	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Dir(stateFile), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(stateFile, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestOfferUpdateStaysSilentAndWritesNothingWhenStderrIsNotATerminal(t *testing.T) {
	captured, stateFile := isolate(t, false)
	OfferUpdate([]string{"instance", "list"})

	if captured.String() != "" {
		t.Errorf("stderr = %q, want empty", captured.String())
	}

	if _, err := os.Stat(stateFile); err == nil {
		t.Errorf("state file %s was written", stateFile)
	}
}

func TestOfferUpdateTellsAnInstallNpmCannotUpdateInPlaceAboutANewerVersion(t *testing.T) {
	captured, stateFile := isolate(t, true)
	knownLatest(t, stateFile, "99.0.0", "")
	OfferUpdate([]string{"instance", "list"})

	want := "\nUpdate available: " + version.Version + " → 99.0.0. Run `featherless-cloud upgrade`.\n\n"
	if captured.String() != want {
		t.Errorf("stderr = %q, want %q", captured.String(), want)
	}
}

func TestOfferUpdateDoesNotOfferAVersionThePersonDeclined(t *testing.T) {
	captured, stateFile := isolate(t, true)
	knownLatest(t, stateFile, "99.0.0", "99.0.0")
	OfferUpdate([]string{"instance", "list"})

	if captured.String() != "" {
		t.Errorf("stderr = %q, want empty", captured.String())
	}
}

func TestOfferUpdateOffersAVersionNewerThanTheDeclinedOne(t *testing.T) {
	captured, stateFile := isolate(t, true)
	knownLatest(t, stateFile, "99.1.0", "99.0.0")
	OfferUpdate([]string{"instance", "list"})

	if !strings.Contains(captured.String(), "99.1.0") {
		t.Errorf("stderr = %q, want it to mention 99.1.0", captured.String())
	}
}

func TestOfferUpdateNeverInterruptsScriptsCompletionOrUpgradeItself(t *testing.T) {
	captured, stateFile := isolate(t, true)
	knownLatest(t, stateFile, "99.0.0", "")

	OfferUpdate([]string{"instance", "list", "--json"})
	OfferUpdate([]string{"--non-interactive", "volume", "ls"})
	OfferUpdate([]string{"_complete", "instance"})
	OfferUpdate([]string{"upgrade"})

	if captured.String() != "" {
		t.Errorf("stderr = %q, want empty", captured.String())
	}
}

var npmGlobalRoot = filepath.Join(string(filepath.Separator), "usr", "local", "lib", "node_modules")

func TestIsGlobalNpmInstallAcceptsThePackageInsideNpmsGlobalFolder(t *testing.T) {
	executable := filepath.Join(npmGlobalRoot, "@featherlessai", "gpu-cloud-cli", "dist", "index.js")

	if !IsGlobalNpmInstall(executable, npmGlobalRoot) {
		t.Errorf("IsGlobalNpmInstall(%q) = false, want true", executable)
	}
}

func TestIsGlobalNpmInstallRefusesNpxCachesProjectInstallsAndOtherPackages(t *testing.T) {
	separator := string(filepath.Separator)
	npx := filepath.Join(
		separator, "home", "ada", ".npm", "_npx", "abc",
		"node_modules", "@featherlessai", "gpu-cloud-cli", "dist", "index.js",
	)
	project := filepath.Join(
		separator, "work", "app",
		"node_modules", "@featherlessai", "gpu-cloud-cli", "dist", "index.js",
	)
	other := filepath.Join(npmGlobalRoot, "@featherlessai", "gpu-cloud-cli-extra", "index.js")

	for _, executable := range []string{npx, project, other} {
		if IsGlobalNpmInstall(executable, npmGlobalRoot) {
			t.Errorf("IsGlobalNpmInstall(%q) = true, want false", executable)
		}
	}
}

// Go-only: npm's global folder worked out from where the binary sits.

// installBinary lays out the binary as npm installs the package into
// base/node_modules, and returns its path.
func installBinary(t *testing.T, base string) string {
	t.Helper()
	platformPackage := filepath.Join(
		base, "node_modules", "@featherlessai", "gpu-cloud-cli",
		"node_modules", "@featherlessai", "gpu-cloud-cli-linux-x64",
	)
	binary := filepath.Join(platformPackage, "bin", "featherless-cloud")

	if err := os.MkdirAll(filepath.Dir(binary), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(binary, nil, 0o755); err != nil {
		t.Fatal(err)
	}

	return binary
}

// linkCommand adds the command link npm puts in a global prefix.
func linkCommand(t *testing.T, prefix, binary string) {
	t.Helper()
	link := filepath.Join(prefix, "bin", "featherless-cloud")

	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(binary, link); err != nil {
		t.Fatal(err)
	}
}

func realTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	return dir
}

func unsetNpmPrefix(t *testing.T) {
	t.Helper()
	t.Setenv("npm_config_prefix", "")
	os.Unsetenv("npm_config_prefix")
}

func TestDetectInstallFindsNpmsGlobalFolderFromTheBinarysPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("npm's Unix layout")
	}

	unsetNpmPrefix(t)
	linked := realTempDir(t)
	linkedBinary := installBinary(t, filepath.Join(linked, "lib"))
	linkCommand(t, linked, linkedBinary)
	linkedMethod := DetectInstall(linkedBinary, npmGlobalRoots(linkedBinary))

	if linkedMethod != InstallNpmGlobal {
		t.Errorf("with npm's command link: got %q, want %q", linkedMethod, InstallNpmGlobal)
	}

	configured := realTempDir(t)
	configuredBinary := installBinary(t, filepath.Join(configured, "lib"))
	t.Setenv("npm_config_prefix", configured)
	configuredMethod := DetectInstall(configuredBinary, npmGlobalRoots(configuredBinary))

	if configuredMethod != InstallNpmGlobal {
		t.Errorf("under npm_config_prefix: got %q, want %q", configuredMethod, InstallNpmGlobal)
	}
}

func TestDetectInstallRefusesProjectInstallsNpxCachesAndAnotherNpmPrefix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("npm's Unix layout")
	}

	unsetNpmPrefix(t)
	home := realTempDir(t)
	project := installBinary(t, filepath.Join(home, "work", "app"))
	npx := installBinary(t, filepath.Join(home, ".npm", "_npx", "abc"))
	unlinked := installBinary(t, filepath.Join(home, "unlinked", "lib"))

	for _, binary := range []string{project, npx, unlinked} {
		method := DetectInstall(binary, npmGlobalRoots(binary))

		if method != InstallOther {
			t.Errorf("DetectInstall(%q) = %q, want %q", binary, method, InstallOther)
		}
	}

	elsewhere := installBinary(t, filepath.Join(home, "elsewhere", "lib"))
	linkCommand(t, filepath.Join(home, "elsewhere"), elsewhere)
	t.Setenv("npm_config_prefix", realTempDir(t))
	elsewhereMethod := DetectInstall(elsewhere, npmGlobalRoots(elsewhere))

	if elsewhereMethod != InstallOther {
		t.Errorf("outside npm_config_prefix: got %q, want %q", elsewhereMethod, InstallOther)
	}
}

// Go-only: the registry lookup the upgrade command shows errors from.

func serveRegistry(t *testing.T, status int, body string) {
	t.Helper()
	latestPath := "/" + version.PackageName + "/latest"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != latestPath {
			http.NotFound(w, r)
			return
		}

		w.WriteHeader(status)
		io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)

	swap(t, &registryBase, server.URL)
}

func TestFetchLatestVersionReturnsThePublishedVersion(t *testing.T) {
	serveRegistry(t, http.StatusOK, `{"name":"@featherlessai/gpu-cloud-cli","version":"0.2.0"}`)

	latest, err := FetchLatestVersion(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if latest != "0.2.0" {
		t.Errorf("latest = %q, want 0.2.0", latest)
	}
}

func TestFetchLatestVersionReportsAFailedLookupStatus(t *testing.T) {
	serveRegistry(t, http.StatusServiceUnavailable, `{}`)

	_, err := FetchLatestVersion(context.Background())
	if err == nil {
		t.Fatal("FetchLatestVersion succeeded, want an error")
	}

	want := "Release lookup failed with status 503."
	if err.Error() != want {
		t.Errorf("err = %q, want %q", err.Error(), want)
	}
}

func TestFetchLatestVersionRejectsAMalformedIndex(t *testing.T) {
	serveRegistry(t, http.StatusOK, `{"version":3}`)

	_, err := FetchLatestVersion(context.Background())
	if err == nil {
		t.Fatal("FetchLatestVersion succeeded, want an error")
	}

	want := "Release index is malformed."
	if err.Error() != want {
		t.Errorf("err = %q, want %q", err.Error(), want)
	}
}

func TestFetchLatestVersionReportsAnUnreachableRegistry(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	unreachable := server.URL
	server.Close()
	swap(t, &registryBase, unreachable)

	_, err := FetchLatestVersion(context.Background())
	if err == nil {
		t.Fatal("FetchLatestVersion succeeded, want an error")
	}

	want := "Could not reach the npm registry."
	if err.Error() != want {
		t.Errorf("err = %q, want %q", err.Error(), want)
	}
}
