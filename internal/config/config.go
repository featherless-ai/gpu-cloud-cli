package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"go.yaml.in/yaml/v3"

	"featherless.ai/featherless-cloud/internal/errs"
)

type Profile struct {
	APIBase   string `yaml:"apiBase"`
	APIKey    string `yaml:"apiKey"`
	CreatedAt string `yaml:"createdAt"`
	Email     string `yaml:"email,omitempty"`
}

type NamedProfile struct {
	Name    string
	Profile Profile
}

// Profiles keeps the file's order, as the config is rewritten on every login.
type Profiles []NamedProfile

type Config struct {
	Version        int      `yaml:"version"`
	CurrentProfile string   `yaml:"currentProfile"`
	Profiles       Profiles `yaml:"profiles"`
}

type ResolvedAuth struct {
	APIBase string
	APIKey  string
	Profile string
}

func (p Profiles) Get(name string) (Profile, bool) {
	for _, entry := range p {
		if entry.Name == name {
			return entry.Profile, true
		}
	}

	return Profile{}, false
}

// Set replaces the named profile in place, or appends it.
func (p *Profiles) Set(name string, profile Profile) {
	for i, entry := range *p {
		if entry.Name == name {
			(*p)[i].Profile = profile
			return
		}
	}

	*p = append(*p, NamedProfile{Name: name, Profile: profile})
}

func (p *Profiles) Delete(name string) {
	out := (*p)[:0]

	for _, entry := range *p {
		if entry.Name != name {
			out = append(out, entry)
		}
	}

	*p = out
}

func (p Profiles) Names() []string {
	names := make([]string, len(p))

	for i, entry := range p {
		names[i] = entry.Name
	}

	return names
}

// yaml.v3 follows YAML 1.1 and quotes timestamp-like strings; the TypeScript
// CLI wrote YAML 1.2, which has no timestamps, so createdAt stays plain.
var yaml11Timestamp = regexp.MustCompile(`^\d{4}-\d\d?-\d\d?([Tt ].*)?$`)

func (p Profile) MarshalYAML() (any, error) {
	node := &yaml.Node{Kind: yaml.MappingNode}
	fields := [][2]string{{"apiBase", p.APIBase}, {"apiKey", p.APIKey}, {"createdAt", p.CreatedAt}}

	if p.Email != "" {
		fields = append(fields, [2]string{"email", p.Email})
	}

	for _, field := range fields {
		value := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: field[1]}

		if yaml11Timestamp.MatchString(field[1]) {
			value.Tag = ""
		}

		node.Content = append(node.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: field[0]}, value)
	}

	return node, nil
}

func (p Profiles) MarshalYAML() (any, error) {
	node := &yaml.Node{Kind: yaml.MappingNode, Style: yaml.FlowStyle}

	if len(p) > 0 {
		node.Style = 0
	}

	for _, entry := range p {
		value := &yaml.Node{}

		if err := value.Encode(entry.Profile); err != nil {
			return nil, err
		}

		node.Content = append(node.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: entry.Name}, value)
	}

	return node, nil
}

func APIBaseURL() string {
	if url := os.Getenv("FEATHERLESS_CLOUD_API_URL"); url != "" {
		return url
	}

	return "https://api.cloud.featherless.ai"
}

func ConsoleBaseURL() string {
	if url := os.Getenv("FEATHERLESS_CLOUD_CONSOLE_URL"); url != "" {
		return url
	}

	return "https://console.cloud.featherless.ai"
}

// Dir is ~/.config on macOS and Linux and %APPDATA% (Roaming) on Windows.
// APPDATA is read because the AppData path can be redirected.
func Dir() string {
	if runtime.GOOS == "windows" {
		if appData := os.Getenv("APPDATA"); appData != "" {
			return appData
		}
	}

	return filepath.Join(homeDir(), ".config")
}

func homeDir() string {
	if home := os.Getenv("HOME"); home != "" {
		return home
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}

	return home
}

func FilePath() string {
	return filepath.Join(Dir(), "featherless-cloud.yaml")
}

func empty() *Config {
	return &Config{Version: 1, CurrentProfile: "", Profiles: Profiles{}}
}

func Load() (*Config, error) {
	file := FilePath()
	raw, err := os.ReadFile(file)

	if err != nil {
		return empty(), nil
	}

	invalid := errs.New(fmt.Sprintf("Config file %s is not valid featherless-cloud config.", file))

	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil || len(doc.Content) != 1 {
		return nil, invalid
	}

	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, invalid
	}

	version := mappingValue(root, "version")
	current := mappingValue(root, "currentProfile")
	profiles := mappingValue(root, "profiles")

	if version == nil || version.Kind != yaml.ScalarNode || version.Tag != "!!int" || version.Value != "1" {
		return nil, invalid
	}

	if current == nil || current.Kind != yaml.ScalarNode || current.Tag != "!!str" {
		return nil, invalid
	}

	if profiles == nil || profiles.Kind != yaml.MappingNode {
		return nil, invalid
	}

	cfg := &Config{Version: 1, CurrentProfile: current.Value, Profiles: Profiles{}}

	for i := 0; i+1 < len(profiles.Content); i += 2 {
		name := profiles.Content[i].Value
		entry := profiles.Content[i+1]

		if !isValidProfile(entry) {
			return nil, invalid
		}

		var profile Profile
		if err := entry.Decode(&profile); err != nil {
			return nil, invalid
		}

		cfg.Profiles = append(cfg.Profiles, NamedProfile{Name: name, Profile: profile})
	}

	return cfg, nil
}

// isValidProfile accepts a profile with a key. apiBase is optional: a profile
// without one, or with an empty one, uses APIBaseURL().
func isValidProfile(entry *yaml.Node) bool {
	if entry.Kind != yaml.MappingNode {
		return false
	}

	key := mappingValue(entry, "apiKey")

	if key == nil || key.Kind != yaml.ScalarNode || key.Tag != "!!str" {
		return false
	}

	base := mappingValue(entry, "apiBase")

	return base == nil || (base.Kind == yaml.ScalarNode && base.Tag == "!!str")
}

func mappingValue(node *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}

	return nil
}

func Save(cfg *Config) error {
	if err := os.MkdirAll(Dir(), 0o755); err != nil {
		return err
	}

	var buf bytes.Buffer
	encoder := yaml.NewEncoder(&buf)
	encoder.SetIndent(2)

	if err := encoder.Encode(cfg); err != nil {
		return err
	}

	if err := encoder.Close(); err != nil {
		return err
	}

	file := FilePath()

	if err := os.WriteFile(file, buf.Bytes(), 0o600); err != nil {
		return err
	}

	return os.Chmod(file, 0o600)
}

// ResolveAuth picks the credentials for a request: the --profile flag, or the
// current profile.
func ResolveAuth(profileFlag string) (ResolvedAuth, error) {
	cfg, err := Load()
	if err != nil {
		return ResolvedAuth{}, err
	}

	if profileFlag != "" {
		profile, ok := cfg.Profiles.Get(profileFlag)

		if !ok {
			return ResolvedAuth{}, errs.Newf("No profile named \"%s\". Run featherless-cloud login --profile %s first.", profileFlag, profileFlag)
		}

		return ResolvedAuth{APIBase: profileAPIBase(profile), APIKey: profile.APIKey, Profile: profileFlag}, nil
	}

	if cfg.CurrentProfile == "" {
		// Inside an instance the CLI signs in with the instance's own key, unless
		// the person signed in with a profile of their own.
		if key, isInstance := InstanceKey(); isInstance {
			return ResolvedAuth{APIBase: APIBaseURL(), APIKey: key, Profile: "instance"}, nil
		}

		return ResolvedAuth{}, errs.New("Not logged in. Run featherless-cloud login.")
	}

	profile, ok := cfg.Profiles.Get(cfg.CurrentProfile)
	if !ok {
		return ResolvedAuth{}, errs.Newf("No profile named \"%s\". Run featherless-cloud login first.", cfg.CurrentProfile)
	}

	return ResolvedAuth{APIBase: profileAPIBase(profile), APIKey: profile.APIKey, Profile: cfg.CurrentProfile}, nil
}

// profileAPIBase is the API host a profile talks to: its own apiBase, or the
// built-in one when the profile leaves it out.
func profileAPIBase(profile Profile) string {
	if profile.APIBase != "" {
		return profile.APIBase
	}

	return APIBaseURL()
}

// InstanceKey returns the API key the platform gives a running instance, if
// this is one.
func InstanceKey() (string, bool) {
	file := os.Getenv("FEATHERLESS_CLOUD_API_KEY_FILE")

	if file == "" {
		return "", false
	}

	raw, err := os.ReadFile(file)
	if err != nil {
		return "", false
	}

	key := strings.TrimSpace(string(raw))
	return key, key != ""
}
