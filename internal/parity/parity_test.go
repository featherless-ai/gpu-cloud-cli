// Package parity runs the TypeScript CLI and the Go CLI side by side against
// the same fake API and compares what each prints, exits with, writes to its
// config and sends to the API. It runs only when both CLIs are given:
//
//	PARITY_TS_CLI=/path/to/dist/index.js PARITY_GO_CLI=/path/to/featherless-cloud go test ./internal/parity/
package parity

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
)

type route struct {
	status int
	body   string
}

type request struct {
	Method         string
	Path           string
	Authorization  string
	ContentType    string
	IdempotencyKey string
	Body           any
}

type fakeAPI struct {
	server   *httptest.Server
	routes   map[string]route
	mu       sync.Mutex
	requests []request
}

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

func newFakeAPI(t *testing.T, routes map[string]route) *fakeAPI {
	api := &fakeAPI{routes: routes}

	api.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body any

		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &body); err != nil {
				body = string(raw)
			}
		}

		key := r.Header.Get("Idempotency-Key")

		if uuidPattern.MatchString(key) {
			key = "<uuid>"
		}

		api.mu.Lock()
		api.requests = append(api.requests, request{
			Method:         r.Method,
			Path:           r.URL.RequestURI(),
			Authorization:  r.Header.Get("Authorization"),
			ContentType:    r.Header.Get("Content-Type"),
			IdempotencyKey: key,
			Body:           body,
		})
		api.mu.Unlock()

		found, ok := api.routes[r.Method+" "+r.URL.RequestURI()]

		if !ok {
			found, ok = api.routes[r.Method+" "+r.URL.Path]
		}

		if !ok {
			found = route{status: 404, body: `{"error":{"code":"not_found","message":"Not found."}}`}
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(found.status)
		_, _ = io.WriteString(w, found.body)
	}))

	t.Cleanup(api.server.Close)
	return api
}

func (a *fakeAPI) take() []request {
	a.mu.Lock()
	defer a.mu.Unlock()
	taken := a.requests
	a.requests = nil
	return taken
}

type result struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Config   string
	Requests []request
}

type scenario struct {
	name string
	args []string
	env  map[string]string
	// config is the featherless-cloud.yaml to start from; "" means logged in
	// to the fake API, "-" means no config file.
	config string
	stdin  string
	routes map[string]route
}

func cliCommand(path string, args []string) *exec.Cmd {
	if strings.HasSuffix(path, ".js") {
		return exec.Command("node", append([]string{path}, args...)...)
	}

	return exec.Command(path, args...)
}

func run(t *testing.T, cli string, s scenario) result {
	t.Helper()
	api := newFakeAPI(t, s.routes)
	home := t.TempDir()
	configDir := filepath.Join(home, ".config")

	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}

	configFile := filepath.Join(configDir, "featherless-cloud.yaml")
	initial := s.config

	if initial == "" {
		initial = fmt.Sprintf("version: 1\ncurrentProfile: work\nprofiles:\n  work:\n    apiBase: %s\n    apiKey: flkey_parity\n    createdAt: 2026-11-01T00:00:00.000Z\n", api.server.URL)
	}

	if initial != "-" {
		if err := os.WriteFile(configFile, []byte(initial), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	cmd := cliCommand(cli, s.args)
	cmd.Env = []string{
		"HOME=" + home,
		"PATH=" + os.Getenv("PATH"),
		"CI=1",
		"NO_COLOR=1",
		"FEATHERLESS_CLOUD_API_URL=" + api.server.URL,
		"FEATHERLESS_CLOUD_CONSOLE_URL=https://console.example.test",
	}

	for name, value := range s.env {
		cmd.Env = append(cmd.Env, name+"="+strings.ReplaceAll(value, "{api}", api.server.URL))
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Stdin = strings.NewReader(s.stdin)

	exitCode := 0

	if err := cmd.Run(); err != nil {
		exitErr, isExit := err.(*exec.ExitError)

		if !isExit {
			t.Fatalf("%s: %v", cli, err)
		}

		exitCode = exitErr.ExitCode()
	}

	written, _ := os.ReadFile(configFile)
	normalize := func(text string) string {
		text = strings.ReplaceAll(text, api.server.URL, "{api}")
		return regexp.MustCompile(`createdAt: \d{4}-\d\d-\d\dT[\d:.]+Z`).ReplaceAllString(text, "createdAt: <now>")
	}

	requests := api.take()

	for i := range requests {
		requests[i].Path = normalize(requests[i].Path)
	}

	return result{
		Stdout:   normalize(stdout.String()),
		Stderr:   normalize(stderr.String()),
		ExitCode: exitCode,
		Config:   normalize(string(written)),
		Requests: requests,
	}
}

func account() map[string]route {
	ok := func(body string) route { return route{status: 200, body: body} }

	routes := map[string]route{
		"GET /me":                                             ok(me),
		"GET /organization":                                   ok(organization),
		"GET /instances":                                      ok(instanceList),
		"GET /instances/rcsrvch012":                           ok(instanceResearch),
		"GET /instances/bx3cpq4567":                           ok(instanceIdle),
		"GET /instances/rcsrvch012/history":                   ok(instanceHistory),
		"GET /instances/rcsrvch012/logs":                      ok(instanceLogs),
		"POST /instances/rcsrvch012/stop":                     ok(instanceResearch),
		"POST /instances/rcsrvch012/delete":                   ok(instanceResearch),
		"POST /instances/":                                    ok(instanceResearch),
		"POST /instances":                                     ok(instanceResearch),
		"PATCH /instances/rcsrvch012":                         ok(instanceResearch),
		"GET /instances/pricing":                              ok(pricing),
		"GET /run-jobs/pricing":                               ok(runJobPricing),
		"GET /templates/":                                     ok(templates),
		"GET /templates":                                      ok(templates),
		"GET /templates/0a1b2c3d-4e5f-4607-8819-2a3b4c5d6e7f": ok(templatePyTorch),
		"GET /templates/2c3d4e5f-6071-4829-8a3b-4c5d6e7f8091": ok(templateMine),
		"GET /volumes/":                                       ok(volumes),
		"GET /volumes":                                        ok(volumes),
		"GET /volumes/9a8b7c6d-5e4f-4a3b-9c2d-1e0f9a8b7c6d":   ok(volumeDatasets),
		"GET /run-jobs":                                       ok(runJobList),
		"GET /run-jobs/jb7xk2mq9z":                            ok(runJob),
		"GET /run-jobs/jb7xk2mq9z/logs":                       ok(runJobLogs),
		"GET /run-jobs/jb7xk2mq9z/logs/history":               ok(runJobLogHistory),
		"POST /run-jobs":                                      ok(runJob),
		"POST /run-jobs/":                                     ok(runJob),
		"GET /me/ssh-keys":                                    ok(sshKeys),
		"GET /registry-credentials":                           ok(registryCredentials),
		"GET /billing":                                        ok(billing),
		"GET /billing/ledger":                                 ok(ledger),
	}

	// Commands that resolve a name call the API with the full id.
	ids := map[string]string{
		"/instances/rcsrvch012": "/instances/6f1c2d3e-4a5b-4c6d-8e7f-901a2b3c4d5e",
		"/instances/bx3cpq4567": "/instances/7a2b3c4d-5e6f-4708-9a1b-2c3d4e5f6071",
		"/run-jobs/jb7xk2mq9z":  "/run-jobs/5e6f7081-92a3-44b5-86c7-d8e9f0a1b2c3",
	}

	for key, value := range maps.Clone(routes) {
		for short, full := range ids {
			if strings.Contains(key, short) {
				routes[strings.Replace(key, short, full, 1)] = value
			}
		}
	}

	return routes
}

func with(routes map[string]route, key string, value route) map[string]route {
	copied := map[string]route{}

	for k, v := range routes {
		copied[k] = v
	}

	copied[key] = value
	return copied
}

func scenarios() []scenario {
	both := func(name string, args ...string) []scenario {
		return []scenario{
			{name: name + " (plain)", args: append([]string{"--non-interactive"}, args...), routes: account()},
			{name: name + " (json)", args: append([]string{"--json"}, args...), routes: account()},
		}
	}

	var all []scenario
	all = append(all, scenario{name: "version", args: []string{"--version"}, routes: account()})
	all = append(all, both("instance list", "instance", "list")...)
	all = append(all, both("instance get by name", "instance", "get", "research")...)
	all = append(all, both("instance get by short id", "instance", "get", "bx3cpq4567")...)
	all = append(all, both("instance history", "instance", "history", "rcsrvch012")...)
	all = append(all, both("instance logs", "instance", "logs", "rcsrvch012", "--tail", "5")...)
	all = append(all, both("instance stop", "instance", "stop", "rcsrvch012")...)
	all = append(all, both("instance delete", "instance", "delete", "rcsrvch012", "--yes")...)
	all = append(all, both("instance create", "instance", "create", "--name", "trainer", "--image", "ubuntu:24.04", "--gpu-type", "MI325X", "--gpu-count", "1", "--disk", "100", "--env", "A=1", "--port", "8080", "--volume", "datasets:/data")...)
	all = append(all, both("instance update", "instance", "update", "rcsrvch012", "--name", "renamed", "--port", "none")...)
	all = append(all, both("pricing list", "pricing", "list")...)
	all = append(all, both("pricing list run jobs, all", "pricing", "list", "--workload", "run-job", "--all")...)
	all = append(all, both("templates list", "templates", "list")...)
	all = append(all, both("templates show", "templates", "show", "pytorch")...)
	all = append(all, both("volume get", "volume", "get", "datasets")...)
	all = append(all, both("volume ls", "volume", "ls")...)
	all = append(all, both("runjob list", "runjob", "list")...)
	all = append(all, both("job get", "job", "get", "jb7xk2mq9z")...)
	all = append(all, both("job logs", "job", "logs", "jb7xk2mq9z", "--tail", "10")...)
	all = append(all, both("job logs earlier attempt", "job", "logs", "jb7xk2mq9z", "--attempt", "1")...)
	all = append(all, both("exec detached", "exec", "--detach", "--name", "nightly-eval", "--image", "python:3.12", "--cpu-cores", "4", "--env", "MODE=full", "--", "python", "eval.py")...)
	all = append(all, both("ssh-keys list", "ssh-keys", "list")...)
	all = append(all, both("registry-keys list", "registry-keys", "list")...)
	all = append(all, both("billing settings", "billing", "settings")...)
	all = append(all, both("billing history", "billing", "history")...)
	all = append(all, both("logout", "logout")...)
	all = append(all, both("login with a key", "login", "--key", "flkey_"+strings.Repeat("a", 43), "--profile", "new")...)
	all = append(all, both("login with a malformed key", "login", "--key", "nope")...)
	all = append(all, both("unknown profile", "instance", "list", "--profile", "nope")...)

	notLoggedIn := both("not logged in", "instance", "list")
	for i := range notLoggedIn {
		notLoggedIn[i].config = "-"
	}
	all = append(all, notLoggedIn...)

	for _, s := range both("missing instance", "instance", "get", "nosuch1234") {
		all = append(all, s)
	}

	for _, s := range both("unauthorized", "instance", "list") {
		s.routes = with(account(), "GET /instances", route{status: 401, body: `{"error":{"code":"unauthorized","message":"Invalid API key."}}`})
		all = append(all, s)
	}

	for _, s := range both("insufficient credit", "instance", "stop", "rcsrvch012") {
		s.routes = with(account(), "POST /instances/rcsrvch012/stop", route{status: 409, body: `{"error":{"code":"insufficient_credit","message":"Your balance is too low."}}`})
		all = append(all, s)
	}

	for _, s := range both("validation errors", "instance", "update", "rcsrvch012", "--name", "x") {
		s.routes = with(account(), "PATCH /instances/rcsrvch012", route{status: 422, body: `{"error":{"code":"validation","message":"Invalid request.","fields":[{"field":"name","message":"Too short."}]}}`})
		all = append(all, s)
	}

	for _, s := range both("server down", "billing", "settings") {
		s.routes = with(account(), "GET /billing", route{status: 503, body: "upstream unavailable"})
		all = append(all, s)
	}

	all = append(all,
		scenario{name: "completion bash", args: []string{"completion", "bash"}, routes: account()},
		scenario{name: "completion zsh", args: []string{"completion", "zsh"}, routes: account()},
	)

	lines := []string{
		"featherless-cloud ",
		"featherless-cloud ins",
		"featherless-cloud instance ",
		"featherless-cloud instance get ",
		"featherless-cloud instance get res",
		"featherless-cloud instance create --",
		"featherless-cloud instance create --gpu-type ",
		"featherless-cloud instance create --gpu-count ",
		"featherless-cloud instance create --compute-type=",
		"featherless-cloud instance create --volume ",
		"featherless-cloud job logs jb7xk2mq9z --attempt ",
		"featherless-cloud exec --cpu-cores ",
		"featherless-cloud templates show ",
		"featherless-cloud volume ls vol",
		"featherless-cloud help ",
		"featherless-cloud --profile work instance get ",
		"featherless-cloud completion ",
	}

	for _, line := range lines {
		all = append(all, scenario{
			name:   "complete " + strings.TrimSpace(strings.TrimPrefix(line, "featherless-cloud")),
			args:   []string{"_complete"},
			env:    map[string]string{"COMP_LINE": line, "COMP_POINT": fmt.Sprint(len(line))},
			routes: account(),
		})
	}

	return all
}

func TestGoCLIMatchesTheTypeScriptCLI(t *testing.T) {
	tsCLI := os.Getenv("PARITY_TS_CLI")
	goCLI := os.Getenv("PARITY_GO_CLI")

	if tsCLI == "" || goCLI == "" {
		t.Skip("set PARITY_TS_CLI and PARITY_GO_CLI to compare the two CLIs")
	}

	for _, s := range scenarios() {
		t.Run(s.name, func(t *testing.T) {
			want := run(t, tsCLI, s)

			if os.Getenv("PARITY_SHOW") != "" {
				t.Logf("TypeScript exit %d\n--- stdout\n%s\n--- stderr\n%s\n--- requests\n%s", want.ExitCode, want.Stdout, want.Stderr, describeRequests(want.Requests))
			}

			got := run(t, goCLI, s)

			if s.name == "version" {
				want.Stdout = regexp.MustCompile(`\d+\.\d+\.\d+`).ReplaceAllString(want.Stdout, "<v>")
				got.Stdout = regexp.MustCompile(`\d+\.\d+\.\d+`).ReplaceAllString(got.Stdout, "<v>")
			}

			compare(t, "stdout", want.Stdout, got.Stdout)
			compare(t, "stderr", want.Stderr, got.Stderr)
			compare(t, "config", want.Config, got.Config)

			if want.ExitCode != got.ExitCode {
				t.Errorf("exit code: TypeScript %d, Go %d", want.ExitCode, got.ExitCode)
			}

			compare(t, "requests", describeRequests(want.Requests), describeRequests(got.Requests))
		})
	}
}

func describeRequests(requests []request) string {
	lines := make([]string, len(requests))

	for i, r := range requests {
		body, _ := json.Marshal(r.Body)
		lines[i] = fmt.Sprintf("%s %s auth=%q type=%q key=%q body=%s", r.Method, r.Path, r.Authorization, r.ContentType, r.IdempotencyKey, canonical(body))
	}

	return strings.Join(lines, "\n")
}

// canonical sorts object keys so bodies compare by content, as the API reads them.
func canonical(raw []byte) string {
	var value any

	if err := json.Unmarshal(raw, &value); err != nil {
		return string(raw)
	}

	var write func(v any) string
	write = func(v any) string {
		switch typed := v.(type) {
		case map[string]any:
			keys := make([]string, 0, len(typed))

			for k := range typed {
				keys = append(keys, k)
			}

			sort.Strings(keys)
			parts := make([]string, len(keys))

			for i, k := range keys {
				encoded, _ := json.Marshal(k)
				parts[i] = string(encoded) + ":" + write(typed[k])
			}

			return "{" + strings.Join(parts, ",") + "}"
		case []any:
			parts := make([]string, len(typed))

			for i, item := range typed {
				parts[i] = write(item)
			}

			return "[" + strings.Join(parts, ",") + "]"
		default:
			encoded, _ := json.Marshal(typed)
			return string(encoded)
		}
	}

	return write(value)
}

func compare(t *testing.T, what, want, got string) {
	t.Helper()

	if want != got {
		t.Errorf("%s differs\n--- TypeScript\n%s\n--- Go\n%s", what, want, got)
	}
}
