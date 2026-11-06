// Package update offers newer releases at startup and keeps the release check
// fresh in the background.
package update

import (
	"context"
	"io"
	"os"
	"time"

	"golang.org/x/term"

	"featherless.ai/featherless-cloud/internal/errs"
	"featherless.ai/featherless-cloud/internal/output"
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
	startBackgroundCheck = func() error { return errs.New("not implemented") }
)

// OfferUpdate offers a newer version the last background check found, then
// refreshes that check when it is a day old. An install the CLI can update in
// place, at a terminal, is asked whether to update now; anything else gets a
// one-line notice. Never touches the network itself. Call once at startup
// with os.Args[1:].
func OfferUpdate(argv []string) {
}

// RunUpdateCheck runs inside the detached `_check-update` child and refreshes
// the cached latest version. Failures are swallowed; the next stale run retries.
func RunUpdateCheck(ctx context.Context) {
}

// FetchLatestVersion asks the npm registry for the latest published version.
func FetchLatestVersion(ctx context.Context) (string, error) {
	return "", errs.New("not implemented")
}

// Upgrade installs release the way method updates. The installer's own output
// goes to stdout; pass stderr in --json mode so stdout stays one JSON document.
func Upgrade(method InstallMethod, release string, stdout io.Writer) error {
	return errs.New("not implemented")
}

// IsGlobalNpmInstall reports whether executable is this package inside npm's
// global folder globalRoot.
func IsGlobalNpmInstall(executable, globalRoot string) bool {
	return false
}

// DetectInstall tells how executable (symlinks resolved) was installed, given
// npm's global folders.
func DetectInstall(executable string, npmRoots []string) InstallMethod {
	return ""
}

// CurrentInstall detects how the running binary was installed.
func CurrentInstall() InstallMethod {
	return ""
}
