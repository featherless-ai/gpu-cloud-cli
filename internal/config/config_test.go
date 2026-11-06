package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"featherless.ai/featherless-cloud/internal/errs"
)

// The CLI derives its config dir from HOME, so isolating HOME isolates it.
func isolateHome(t *testing.T) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
}

func TestLoadReturnsEmptyShapeWhenMissing(t *testing.T) {
	isolateHome(t)

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}

	want := &Config{Version: 1, CurrentProfile: "", Profiles: Profiles{}}
	if !reflect.DeepEqual(cfg, want) {
		t.Errorf("got %+v, want %+v", cfg, want)
	}
}

func TestLoadRoundTripsThroughSave(t *testing.T) {
	isolateHome(t)

	cfg := &Config{
		Version:        1,
		CurrentProfile: "default",
		Profiles: Profiles{{
			Name: "default",
			Profile: Profile{
				APIBase:   "https://api.cloud.featherless.ai",
				APIKey:    "flkey_test",
				Email:     "dev@example.test",
				CreatedAt: time.Unix(0, 0).UTC().Format("2006-01-02T15:04:05.000Z"),
			},
		}},
	}

	if err := Save(cfg); err != nil {
		t.Fatal(err)
	}

	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(loaded, cfg) {
		t.Errorf("got %+v, want %+v", loaded, cfg)
	}
}

func TestLoadRejectsMalformedYAMLWithExactMessage(t *testing.T) {
	isolateHome(t)
	file := FilePath()

	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(file, []byte("\tinvalid: [unclosed"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := Load()
	want := "Config file " + file + " is not valid featherless-cloud config."

	if err == nil || err.Error() != want || !errs.IsCLI(err) {
		t.Errorf("got %v, want CLI error %q", err, want)
	}
}

func TestLoadRejectsEntriesMissingAPIBaseOrAPIKey(t *testing.T) {
	isolateHome(t)
	file := FilePath()

	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}

	body := "version: 1\ncurrentProfile: x\nprofiles:\n  x:\n    apiBase: https://a\n"
	if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(); err == nil || !errs.IsCLI(err) {
		t.Errorf("got %v, want a CLI error", err)
	}
}

func TestSaveStoresCredentialsWithMode0600(t *testing.T) {
	isolateHome(t)

	if err := Save(&Config{Version: 1, Profiles: Profiles{}}); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(FilePath())
	if err != nil {
		t.Fatal(err)
	}

	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %o", info.Mode().Perm())
	}
}

// Exactly what the TypeScript CLI's YAML.stringify wrote, so existing config
// files keep working and stay byte-identical when rewritten.
const typeScriptConfig = `version: 1
currentProfile: default
profiles:
  default:
    apiBase: https://api.cloud.featherless.ai
    apiKey: flkey_x
    createdAt: 1970-01-01T00:00:00.000Z
    email: a@b.test
  other:
    apiBase: https://x
    apiKey: flkey_y
    createdAt: 2026-11-06T07:00:00.000Z
`

func TestReadsAndRewritesTheTypeScriptConfigUnchanged(t *testing.T) {
	isolateHome(t)
	file := FilePath()

	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(file, []byte(typeScriptConfig), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}

	if got := cfg.Profiles.Names(); !reflect.DeepEqual(got, []string{"default", "other"}) {
		t.Errorf("profile order = %v", got)
	}

	if p, _ := cfg.Profiles.Get("other"); p.CreatedAt != "2026-11-06T07:00:00.000Z" {
		t.Errorf("createdAt = %q", p.CreatedAt)
	}

	if err := Save(cfg); err != nil {
		t.Fatal(err)
	}

	raw, _ := os.ReadFile(file)
	if string(raw) != typeScriptConfig {
		t.Errorf("rewritten config differs:\n%s", raw)
	}
}

func TestWritesTheEmptyConfigLikeTypeScript(t *testing.T) {
	isolateHome(t)

	if err := Save(&Config{Version: 1, Profiles: Profiles{}}); err != nil {
		t.Fatal(err)
	}

	raw, _ := os.ReadFile(FilePath())
	want := "version: 1\ncurrentProfile: \"\"\nprofiles: {}\n"

	if string(raw) != want {
		t.Errorf("got %q, want %q", raw, want)
	}
}
