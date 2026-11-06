package update

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"slices"
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

// Go-only: Homebrew and standalone installs.

func TestDetectInstallRecognisesHomebrewsCellar(t *testing.T) {
	separator := string(filepath.Separator)
	prefixes := [][]string{
		{"opt", "homebrew"},
		{"usr", "local"},
		{"home", "linuxbrew", ".linuxbrew"},
	}

	for _, prefix := range prefixes {
		parts := append([]string{separator}, prefix...)
		parts = append(parts, "Cellar", "featherless-cloud", "0.2.0", "bin", "featherless-cloud")
		executable := filepath.Join(parts...)
		method := DetectInstall(executable, nil)

		if method != InstallHomebrew {
			t.Errorf("DetectInstall(%q) = %q, want %q", executable, method, InstallHomebrew)
		}
	}
}

func TestDetectInstallTreatsAnyOtherBinaryAsStandalone(t *testing.T) {
	separator := string(filepath.Separator)
	executables := []string{
		filepath.Join(separator, "home", "ada", ".local", "bin", "featherless-cloud"),
		filepath.Join(separator, "usr", "local", "bin", "featherless-cloud"),
	}

	for _, executable := range executables {
		method := DetectInstall(executable, nil)

		if method != InstallStandalone {
			t.Errorf("DetectInstall(%q) = %q, want %q", executable, method, InstallStandalone)
		}
	}
}

func TestPlatformPackageNamesNpmsOsAndArch(t *testing.T) {
	cases := []struct{ goos, goarch, want string }{
		{"darwin", "arm64", "@featherlessai/gpu-cloud-cli-darwin-arm64"},
		{"darwin", "amd64", "@featherlessai/gpu-cloud-cli-darwin-x64"},
		{"linux", "arm64", "@featherlessai/gpu-cloud-cli-linux-arm64"},
		{"linux", "amd64", "@featherlessai/gpu-cloud-cli-linux-x64"},
		{"windows", "amd64", "@featherlessai/gpu-cloud-cli-win32-x64"},
	}

	for _, c := range cases {
		name, err := platformPackage(c.goos, c.goarch)

		if err != nil {
			t.Errorf("platformPackage(%q, %q): %v", c.goos, c.goarch, err)
			continue
		}

		if name != c.want {
			t.Errorf("platformPackage(%q, %q) = %q, want %q", c.goos, c.goarch, name, c.want)
		}
	}

	_, err := platformPackage("freebsd", "amd64")
	if err == nil {
		t.Fatal("platformPackage(freebsd, amd64) succeeded, want an error")
	}

	want := "No featherless-cloud release for freebsd/amd64."
	if err.Error() != want {
		t.Errorf("err = %q, want %q", err.Error(), want)
	}
}

// releaseTarball packs binary the way npm publishes the platform package.
func releaseTarball(t *testing.T, binary []byte) []byte {
	t.Helper()
	binaryName := "package/bin/featherless-cloud"

	if runtime.GOOS == "windows" {
		binaryName += ".exe"
	}

	files := []struct {
		name string
		mode int64
		body []byte
	}{
		{"package/package.json", 0o644, []byte(`{"name":"@featherlessai/gpu-cloud-cli-test"}`)},
		{binaryName, 0o755, binary},
	}

	var archive bytes.Buffer
	compressed := gzip.NewWriter(&archive)
	packed := tar.NewWriter(compressed)

	for _, file := range files {
		header := &tar.Header{Name: file.name, Mode: file.mode, Size: int64(len(file.body)), Typeflag: tar.TypeReg}

		if err := packed.WriteHeader(header); err != nil {
			t.Fatal(err)
		}

		if _, err := packed.Write(file.body); err != nil {
			t.Fatal(err)
		}
	}

	if err := packed.Close(); err != nil {
		t.Fatal(err)
	}

	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}

	return archive.Bytes()
}

func integrityOf(data []byte) string {
	sum := sha512.Sum512(data)
	return "sha512-" + base64.StdEncoding.EncodeToString(sum[:])
}

// serveRelease publishes release's platform package for this OS and arch on a
// test registry, with tarball under the given integrity.
func serveRelease(t *testing.T, release string, tarball []byte, integrity string) {
	t.Helper()
	name, err := platformPackage(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}

	var server *httptest.Server

	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/" + name + "/" + release:
			dist := map[string]string{"tarball": server.URL + "/tarball.tgz", "integrity": integrity}
			index := map[string]any{"name": name, "version": release, "dist": dist}
			json.NewEncoder(w).Encode(index)
		case "/tarball.tgz":
			w.Write(tarball)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	swap(t, &registryBase, server.URL)
}

// installStandalone makes an old binary in a temp folder the running executable.
func installStandalone(t *testing.T) string {
	t.Helper()
	executable := filepath.Join(t.TempDir(), "featherless-cloud")

	if err := os.WriteFile(executable, []byte("old binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	swap(t, &executablePath, func() (string, error) { return executable, nil })
	return executable
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	return string(content)
}

func TestUpgradeReplacesAStandaloneBinaryWithTheRelease(t *testing.T) {
	executable := installStandalone(t)
	tarball := releaseTarball(t, []byte("new binary"))
	serveRelease(t, "0.2.0", tarball, integrityOf(tarball))

	err := Upgrade(context.Background(), InstallStandalone, "0.2.0", io.Discard)
	if err != nil {
		t.Fatal(err)
	}

	content := readFile(t, executable)
	if content != "new binary" {
		t.Errorf("executable holds %q, want %q", content, "new binary")
	}

	info, err := os.Stat(executable)
	if err != nil {
		t.Fatal(err)
	}

	if runtime.GOOS != "windows" {
		if info.Mode().Perm() != 0o755 {
			t.Errorf("mode = %v, want 0755", info.Mode().Perm())
		}
	}

	if _, err := os.Stat(executable + ".new"); err == nil {
		t.Errorf("%s.new was left behind", executable)
	}
}

func TestUpgradeRefusesADownloadThatFailsItsIntegrityCheck(t *testing.T) {
	executable := installStandalone(t)
	tarball := releaseTarball(t, []byte("new binary"))
	published := releaseTarball(t, []byte("a different binary"))
	serveRelease(t, "0.2.0", tarball, integrityOf(published))

	err := Upgrade(context.Background(), InstallStandalone, "0.2.0", io.Discard)
	if err == nil {
		t.Fatal("Upgrade succeeded, want an error")
	}

	want := "Downloaded featherless-cloud 0.2.0 failed its integrity check."
	if err.Error() != want {
		t.Errorf("err = %q, want %q", err.Error(), want)
	}

	content := readFile(t, executable)
	if content != "old binary" {
		t.Errorf("executable holds %q, want it untouched", content)
	}

	if _, err := os.Stat(executable + ".new"); err == nil {
		t.Errorf("%s.new was left behind", executable)
	}
}

func TestUpgradeExplainsAnExecutableItCannotReplace(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix error text")
	}

	// A file where the folder should be fails for every user, root included.
	blocker := filepath.Join(t.TempDir(), "blocker")

	if err := os.WriteFile(blocker, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	executable := filepath.Join(blocker, "featherless-cloud")
	swap(t, &executablePath, func() (string, error) { return executable, nil })
	tarball := releaseTarball(t, []byte("new binary"))
	serveRelease(t, "0.2.0", tarball, integrityOf(tarball))

	err := Upgrade(context.Background(), InstallStandalone, "0.2.0", io.Discard)
	if err == nil {
		t.Fatal("Upgrade succeeded, want an error")
	}

	want := "Can't replace " + executable + ": not a directory. " +
		"Run: curl -fsSL https://console.cloud.featherless.ai/install.sh | sh"
	if err.Error() != want {
		t.Errorf("err = %q, want %q", err.Error(), want)
	}
}

// fakeBrew puts a brew on PATH that records its arguments and exits with code.
func fakeBrew(t *testing.T, code int) string {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("shell script brew")
	}

	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	script := fmt.Sprintf("#!/bin/sh\necho \"$@\" > '%s'\nexit %d\n", argsFile, code)

	if err := os.WriteFile(filepath.Join(dir, "brew"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", dir)
	return argsFile
}

func TestUpgradeRunsBrewForAHomebrewInstall(t *testing.T) {
	argsFile := fakeBrew(t, 0)

	err := Upgrade(context.Background(), InstallHomebrew, "0.2.0", io.Discard)
	if err != nil {
		t.Fatal(err)
	}

	args := readFile(t, argsFile)
	if args != "upgrade featherless-cloud\n" {
		t.Errorf("brew ran with %q, want %q", args, "upgrade featherless-cloud\n")
	}
}

func TestUpgradeTellsHowToRunBrewWhenItFails(t *testing.T) {
	fakeBrew(t, 1)

	err := Upgrade(context.Background(), InstallHomebrew, "0.2.0", io.Discard)
	if err == nil {
		t.Fatal("Upgrade succeeded, want an error")
	}

	want := "brew exited with code 1. Run: brew upgrade featherless-cloud"
	if err.Error() != want {
		t.Errorf("err = %q, want %q", err.Error(), want)
	}
}

// The TypeScript CLI passed on Node's spawn error, which names npm and the errno.
func TestUpgradeSaysNpmCouldNotBeSpawnedTheWayNodeDid(t *testing.T) {
	notExecutable := t.TempDir()
	if err := os.WriteFile(filepath.Join(notExecutable, "npm"), []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		path string
		want string
	}{
		{"missing", t.TempDir(), "Could not run npm: spawn npm ENOENT. Run: npm install --global @featherlessai/gpu-cloud-cli@0.2.0"},
		{"not executable", notExecutable, "Could not run npm: spawn npm EACCES. Run: npm install --global @featherlessai/gpu-cloud-cli@0.2.0"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("PATH", c.path)

			err := Upgrade(context.Background(), InstallNpmGlobal, "0.2.0", io.Discard)
			if err == nil || err.Error() != c.want {
				t.Errorf("err = %v, want %q", err, c.want)
			}
		})
	}
}

func TestUpgradeSaysNpmExitedWithCodeNullWhenASignalStoppedIt(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "npm"), []byte("#!/bin/sh\nkill -TERM $$\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+":/bin:/usr/bin")

	err := Upgrade(context.Background(), InstallNpmGlobal, "0.2.0", io.Discard)

	want := "npm exited with code null. Run: npm install --global @featherlessai/gpu-cloud-cli@0.2.0"
	if err == nil || err.Error() != want {
		t.Errorf("err = %v, want %q", err, want)
	}
}

type answerPrompter struct {
	output.Prompter
	answer   bool
	messages *[]string
}

func (p answerPrompter) Confirm(message string, initial bool) (bool, error) {
	*p.messages = append(*p.messages, message)
	return p.answer, nil
}

// atTerminal stands in for a person at a terminal running an install of method.
func atTerminal(t *testing.T, method InstallMethod, answer bool) *[]string {
	t.Helper()
	var messages []string
	swap(t, &stdinIsTerminal, func() bool { return true })
	swap(t, &currentInstall, func() InstallMethod { return method })
	swap(t, &prompter, output.Prompter(answerPrompter{answer: answer, messages: &messages}))
	return &messages
}

func TestOfferUpdatePointsHomebrewInstallsAtBrew(t *testing.T) {
	captured, stateFile := isolate(t, true)
	prompts := atTerminal(t, InstallHomebrew, true)
	knownLatest(t, stateFile, "99.0.0", "")
	OfferUpdate([]string{"instance", "list"})

	want := "\nUpdate available: " + version.Version + " → 99.0.0. Run `brew upgrade featherless-cloud`.\n\n"
	if captured.String() != want {
		t.Errorf("stderr = %q, want %q", captured.String(), want)
	}

	if len(*prompts) != 0 {
		t.Errorf("asked %q, want no prompt", *prompts)
	}
}

func TestOfferUpdateAsksAStandaloneInstallAtATerminal(t *testing.T) {
	captured, stateFile := isolate(t, true)
	prompts := atTerminal(t, InstallStandalone, false)
	knownLatest(t, stateFile, "99.0.0", "")
	OfferUpdate([]string{"instance", "list"})

	wantPrompts := []string{"featherless-cloud 99.0.0 is available (you have " + version.Version + "). Update now?"}
	if !slices.Equal(*prompts, wantPrompts) {
		t.Errorf("asked %q, want %q", *prompts, wantPrompts)
	}

	wantStderr := "Run `featherless-cloud upgrade` whenever you want it.\n\n"
	if captured.String() != wantStderr {
		t.Errorf("stderr = %q, want %q", captured.String(), wantStderr)
	}

	var state map[string]any
	if err := json.Unmarshal([]byte(readFile(t, stateFile)), &state); err != nil {
		t.Fatal(err)
	}

	if state["declined"] != "99.0.0" {
		t.Errorf("declined = %v, want 99.0.0", state["declined"])
	}
}

func TestOfferUpdateSelfUpdatesAStandaloneInstallOnYes(t *testing.T) {
	captured, stateFile := isolate(t, true)
	atTerminal(t, InstallStandalone, true)
	executable := installStandalone(t)
	tarball := releaseTarball(t, []byte("new binary"))
	serveRelease(t, "99.0.0", tarball, integrityOf(tarball))
	knownLatest(t, stateFile, "99.0.0", "")
	OfferUpdate([]string{"instance", "list"})

	want := "\nUpdated featherless-cloud to 99.0.0.\n\n"
	if captured.String() != want {
		t.Errorf("stderr = %q, want %q", captured.String(), want)
	}

	content := readFile(t, executable)
	if content != "new binary" {
		t.Errorf("executable holds %q, want %q", content, "new binary")
	}
}
