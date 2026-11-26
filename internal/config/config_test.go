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

func TestLoadRejectsProfilesWithoutAnAPIKey(t *testing.T) {
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

// writeProfile stores one profile with the given body, as hand-edited.
func writeProfile(t *testing.T, profileBody string) {
	t.Helper()
	file := FilePath()

	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}

	body := "version: 1\ncurrentProfile: work\nprofiles:\n  work:\n" + profileBody

	if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestProfileWithoutAPIBaseUsesTheDefaultHost(t *testing.T) {
	isolateHome(t)
	t.Setenv("FEATHERLESS_CLOUD_API_URL", "")
	writeProfile(t, "    apiKey: flkey_test\n")

	auth, err := ResolveAuth("")
	if err != nil {
		t.Fatal(err)
	}

	if auth.APIBase != "https://api.cloud.featherless.ai" {
		t.Errorf("apiBase = %q, want the built-in host", auth.APIBase)
	}
}

func TestProfileWithoutAPIBaseFollowsTheEnvironment(t *testing.T) {
	isolateHome(t)
	t.Setenv("FEATHERLESS_CLOUD_API_URL", "https://api.example.test")
	writeProfile(t, "    apiKey: flkey_test\n")

	auth, err := ResolveAuth("work")
	if err != nil {
		t.Fatal(err)
	}

	if auth.APIBase != "https://api.example.test" {
		t.Errorf("apiBase = %q, want the environment's host", auth.APIBase)
	}
}

func TestStoredAPIBaseWins(t *testing.T) {
	isolateHome(t)
	t.Setenv("FEATHERLESS_CLOUD_API_URL", "https://api.example.test")
	writeProfile(t, "    apiBase: https://api.staging.cloud.featherless.ai\n    apiKey: flkey_test\n")

	auth, err := ResolveAuth("work")
	if err != nil {
		t.Fatal(err)
	}

	if auth.APIBase != "https://api.staging.cloud.featherless.ai" {
		t.Errorf("apiBase = %q, want the profile's own host", auth.APIBase)
	}
}

func TestEmptyAPIBaseFallsBack(t *testing.T) {
	isolateHome(t)
	t.Setenv("FEATHERLESS_CLOUD_API_URL", "")
	writeProfile(t, "    apiBase: \"\"\n    apiKey: flkey_test\n")

	auth, err := ResolveAuth("work")
	if err != nil {
		t.Fatal(err)
	}

	if auth.APIBase != "https://api.cloud.featherless.ai" {
		t.Errorf("apiBase = %q, want the built-in host", auth.APIBase)
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

// Inside an instance the platform puts a key in a file and names it in
// FEATHERLESS_CLOUD_API_KEY_FILE; the CLI signs in with it unless the person
// chose a profile.
func instanceKey(t *testing.T, key string) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "api-key")

	if err := os.WriteFile(file, []byte(key+"\n"), 0o444); err != nil {
		t.Fatal(err)
	}

	t.Setenv("FEATHERLESS_CLOUD_API_KEY_FILE", file)
	t.Setenv("FEATHERLESS_CLOUD_API_URL", "https://api.example.test")
}

func savedProfile(t *testing.T) {
	t.Helper()

	cfg := &Config{Version: 1, CurrentProfile: "work", Profiles: Profiles{{
		Name:    "work",
		Profile: Profile{APIBase: "https://api.work.test", APIKey: "flkey_work", CreatedAt: "2026-11-01T00:00:00.000Z"},
	}}}

	if err := Save(cfg); err != nil {
		t.Fatal(err)
	}
}

func TestResolveAuthSignsInWithTheInstanceKeyWhenNoProfileIsChosen(t *testing.T) {
	isolateHome(t)
	instanceKey(t, "flkey_instance")

	auth, err := ResolveAuth("")
	if err != nil {
		t.Fatal(err)
	}

	want := ResolvedAuth{APIBase: "https://api.example.test", APIKey: "flkey_instance", Profile: "instance"}
	if auth != want {
		t.Errorf("got %+v, want %+v", auth, want)
	}
}

func TestResolveAuthPrefersTheCurrentProfileOverTheInstanceKey(t *testing.T) {
	isolateHome(t)
	instanceKey(t, "flkey_instance")
	savedProfile(t)

	auth, err := ResolveAuth("")
	if err != nil {
		t.Fatal(err)
	}

	if auth.APIKey != "flkey_work" || auth.APIBase != "https://api.work.test" {
		t.Errorf("got %+v, want the work profile", auth)
	}
}

func TestResolveAuthPrefersAnExplicitProfileOverTheInstanceKey(t *testing.T) {
	isolateHome(t)
	instanceKey(t, "flkey_instance")

	if _, err := ResolveAuth("missing"); err == nil || err.Error() != `No profile named "missing". Run featherless-cloud login --profile missing first.` {
		t.Errorf("got %v", err)
	}
}

func TestResolveAuthIsNotLoggedInWhenTheInstanceKeyFileIsMissingOrEmpty(t *testing.T) {
	isolateHome(t)
	t.Setenv("FEATHERLESS_CLOUD_API_KEY_FILE", filepath.Join(t.TempDir(), "nothing-here"))

	if _, err := ResolveAuth(""); err == nil || err.Error() != "Not logged in. Run featherless-cloud login." {
		t.Errorf("missing file: got %v", err)
	}

	instanceKey(t, "  ")

	if _, err := ResolveAuth(""); err == nil || err.Error() != "Not logged in. Run featherless-cloud login." {
		t.Errorf("empty file: got %v", err)
	}
}

func TestInstanceKeyReportsWhetherTheInstanceSignsIn(t *testing.T) {
	isolateHome(t)

	if _, ok := InstanceKey(); ok {
		t.Error("no instance key expected outside an instance")
	}

	instanceKey(t, "flkey_instance")

	if key, ok := InstanceKey(); !ok || key != "flkey_instance" {
		t.Errorf("got %q, %v", key, ok)
	}
}
