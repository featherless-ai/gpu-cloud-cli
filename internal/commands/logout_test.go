package commands

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"

	"featherless.ai/featherless-cloud/internal/commands/shared"
	"featherless.ai/featherless-cloud/internal/output"
)

func swap[T any](t *testing.T, target *T, value T) {
	t.Helper()
	previous := *target
	*target = value
	t.Cleanup(func() { *target = previous })
}

// signedOut has no config file; insideInstance gives this computer an
// instance key, as the platform does.
func signedOut(t *testing.T, insideInstance bool) *bytes.Buffer {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("FEATHERLESS_CLOUD_API_KEY_FILE", "")

	if insideInstance {
		keyFile := filepath.Join(home, "api-key")

		if err := os.WriteFile(keyFile, []byte("flkey_instance\n"), 0o600); err != nil {
			t.Fatal(err)
		}

		t.Setenv("FEATHERLESS_CLOUD_API_KEY_FILE", keyFile)
	}

	var stdout bytes.Buffer

	swap(t, &shared.NewContext, func(cmd *cobra.Command) *output.Context {
		ctx := output.FromCommand(cmd)
		ctx.Stdout = &stdout
		ctx.Stderr = &stdout

		return ctx
	})

	return &stdout
}

func execute(args ...string) error {
	root := NewRoot()
	root.SetArgs(args)

	return root.Execute()
}

func TestLogoutInsideAnInstanceSaysItSignsInAutomatically(t *testing.T) {
	stdout := signedOut(t, true)

	if err := execute("--non-interactive", "logout"); err != nil {
		t.Fatalf("error = %v, want success", err)
	}

	if got := stdout.String(); got != "This instance signs in automatically.\n" {
		t.Errorf("output = %q", got)
	}
}

func TestLogoutOfAMissingProfileInsideAnInstanceIsNotLoggedIn(t *testing.T) {
	signedOut(t, true)
	err := execute("--non-interactive", "logout", "--profile", "nope")

	if err == nil || err.Error() != "Not logged in." {
		t.Errorf("error = %v, want Not logged in.", err)
	}
}

func TestLogoutOutsideAnInstanceIsNotLoggedIn(t *testing.T) {
	signedOut(t, false)
	err := execute("--non-interactive", "logout")

	if err == nil || err.Error() != "Not logged in." {
		t.Errorf("error = %v, want Not logged in.", err)
	}
}
