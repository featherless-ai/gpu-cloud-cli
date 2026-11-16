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
	all = append(all, both("volume ls human", "volume", "ls", "-h")...)
	all = append(all, both("volume create too small", "volume", "create", "scratch", "--gb", "5")...)
	all = append(all, both("volume update nothing", "volume", "update", "datasets")...)
	all = append(all, both("volume update shrink", "volume", "update", "datasets", "--gb", "100")...)
	all = append(all, both("volume rm without yes", "volume", "rm", "vol://datasets/old.bin")...)
	all = append(all, both("volume rm root", "volume", "rm", "vol://datasets/")...)
	all = append(all, both("volume cp one path", "volume", "cp", "vol://datasets/a")...)
	all = append(all, both("volume cp local only", "volume", "cp", "./a", "./b")...)
	all = append(all, both("volume cp mixed sources", "volume", "cp", "./a", "vol://datasets/a", "vol://datasets/")...)
	all = append(all, both("volume cp bad concurrency", "volume", "cp", "./a", "vol://datasets/", "--concurrency", "0")...)
	all = append(all, both("volume mv root", "volume", "mv", "vol://datasets", "vol://datasets/x")...)
	all = append(all, both("volume find name and iname", "volume", "find", "vol://datasets", "--name", "a", "--iname", "b")...)
	all = append(all, both("volume find bad size", "volume", "find", "vol://datasets", "--size", "10G")...)
	all = append(all, both("volume find bad type", "volume", "find", "vol://datasets", "--type", "x")...)
	all = append(all, both("volume sync bad mode", "volume", "sync", ".", "vol://datasets/", "--mode", "sideways")...)
	all = append(all, both("volume get unknown", "volume", "get", "nosuch")...)

	volumeChanges := []struct {
		scenario []scenario
		key      string
		answer   route
	}{
		{both("volume create", "volume", "create", "scratch", "--gb", "50"), "POST /volumes/", route{status: 201, body: volumeDatasets}},
		{both("volume update rename", "volume", "update", "datasets", "--name", "renamed"), "PATCH /volumes/9a8b7c6d-5e4f-4a3b-9c2d-1e0f9a8b7c6d", route{status: 200, body: volumeDatasets}},
		{both("volume delete", "volume", "delete", "datasets", "--yes"), "DELETE /volumes/9a8b7c6d-5e4f-4a3b-9c2d-1e0f9a8b7c6d?detach=true", route{status: 202, body: ""}},
	}

	for _, change := range volumeChanges {
		for _, s := range change.scenario {
			s.routes = with(account(), change.key, change.answer)
			all = append(all, s)
		}
	}

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

	all = append(all, commandScenarios()...)

	return all
}

// improvedOutput names the scenarios whose stdout the Go CLI changed on
// purpose, and why. The rest of each run (stderr, exit code, config, requests)
// still has to match.
var improvedOutput = map[string]string{
	"billing history (plain)":         "charges read -$2.51, not $-2.512345",
	"billing history metered (plain)": "charges read -$0.0045, not $-0.0045",
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

			if _, isImproved := improvedOutput[s.name]; !isImproved {
				compare(t, "stdout", want.Stdout, got.Stdout)
			}

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

// commandScenarios cover the instance, Run Job, template, key and billing
// commands that change things or report errors.
func commandScenarios() []scenario {
	ok := func(body string) route { return route{status: 200, body: body} }
	noContent := route{status: 204, body: ""}
	research := "/instances/6f1c2d3e-4a5b-4c6d-8e7f-901a2b3c4d5e"

	both := func(name string, routes map[string]route, args ...string) []scenario {
		return []scenario{
			{name: name + " (plain)", args: append([]string{"--non-interactive"}, args...), routes: routes},
			{name: name + " (json)", args: append([]string{"--json"}, args...), routes: routes},
		}
	}

	routes := account()
	routes["POST /instances/rcsrvch012/start"] = ok(instanceResearch)
	routes["POST "+research+"/restart"] = ok(instanceResearch)
	routes["POST /run-jobs/jb7xk2mq9z/cancel"] = ok(runJob)
	routes["DELETE /run-jobs/jb7xk2mq9z"] = ok(runJob)
	routes["POST /templates/"] = ok(templateMine)
	routes["PATCH /templates/2c3d4e5f-6071-4829-8a3b-4c5d6e7f8091"] = ok(templateMine)
	routes["DELETE /templates/2c3d4e5f-6071-4829-8a3b-4c5d6e7f8091"] = noContent
	routes["POST /me/ssh-keys"] = ok(sshKeyAdded)
	routes["DELETE /me/ssh-keys/5f607182-93a4-45b6-87c8-d9e0f1a2b3c4"] = noContent
	routes["POST /registry-credentials"] = ok(registryCredentialAdded)
	routes["PATCH /registry-credentials/4c5d6e7f-8091-4a2b-8c3d-4e5f60718293"] = ok(registryCredentialAdded)
	routes["DELETE /registry-credentials/4c5d6e7f-8091-4a2b-8c3d-4e5f60718293"] = noContent

	var all []scenario
	all = append(all, both("instance start", routes, "instance", "start", "rcsrvch012")...)
	all = append(all, both("instance restart by name", routes, "instance", "restart", "research")...)
	all = append(all, both("instance delete without --yes", routes, "instance", "delete", "rcsrvch012")...)
	all = append(all, both("instance update nothing", routes, "instance", "update", "rcsrvch012")...)
	all = append(all, both("instance update everything", routes, "instance", "update", "rcsrvch012", "--image", " ghcr.io/acme/tool:2 ", "--registry", "GHCR", "--disk", "300", "--command", "  python  serve.py ", "--env", "A=1", "--env", "B=x=y", "--port", "8080", "--port", "9090")...)
	all = append(all, both("instance update public pulls", routes, "instance", "update", "rcsrvch012", "--registry", "null", "--command", "")...)
	all = append(all, both("instance update bad port", routes, "instance", "update", "rcsrvch012", "--port", "8080", "--port", "8080")...)
	all = append(all, both("instance logs head", routes, "instance", "logs", "rcsrvch012", "--head", "--tail", "3")...)
	all = append(all, both("instance logs bad tail", routes, "instance", "logs", "rcsrvch012", "--tail", "0")...)
	all = append(all, both("instance logs head and follow", routes, "instance", "logs", "rcsrvch012", "--head", "--follow")...)
	all = append(all, both("instance create from a template", routes, "instance", "create", "--name", "trainer", "--template", "my-trainer")...)
	all = append(all, both("instance create official version", routes, "instance", "create", "--name", "trainer", "--template", "pytorch:2.3.1", "--gpu-count", "8", "--disk", "500")...)
	all = append(all, both("instance create unknown version", routes, "instance", "create", "--name", "trainer", "--template", "pytorch:9.9")...)
	all = append(all, both("instance create cpu", routes, "instance", "create", "--name", "cpu-box", "--image", "ubuntu:24.04", "--compute-type", "cpu", "--cpu-cores", "8", "--secret", "TOKEN=x", "--command", "sleep infinity")...)
	all = append(all, both("instance create unknown cpu size", routes, "instance", "create", "--name", "cpu-box", "--image", "ubuntu:24.04", "--compute-type", "cpu", "--cpu-cores", "6")...)
	all = append(all, both("instance create bad name", routes, "instance", "create", "--name", "Bad_Name", "--image", "ubuntu:24.04")...)
	all = append(all, both("instance create registry without image", routes, "instance", "create", "--name", "trainer", "--registry", "ghcr")...)
	all = append(all, both("instance create bad volume", routes, "instance", "create", "--name", "trainer", "--image", "ubuntu:24.04", "--volume", "datasets")...)

	atLimit := with(routes, "GET /organization", ok(`{"data": {"organization": {"id": "o1", "name": "Acme", "slug": "acme"}, "membership": {"id": "m1", "role": "owner"}, "gpuCount": 8, "gpuLimit": 8, "cpuCount": 0, "cpuLimit": 128}}`))
	all = append(all, both("instance create at the GPU limit", atLimit, "instance", "create", "--name", "trainer", "--image", "ubuntu:24.04")...)

	starting := with(routes, "GET /instances/rcsrvch012", ok(instanceStarting))
	all = append(all, both("instance get while starting", starting, "instance", "get", "rcsrvch012")...)

	all = append(all, scenario{name: "exec attached", args: []string{"--non-interactive", "exec", "--name", "nightly-eval", "--cpu-cores", "4", "--", "python", "eval.py"}, routes: routes})
	all = append(all, scenario{name: "exec json without detach", args: []string{"--json", "exec", "--cpu-cores", "4", "--", "python", "eval.py"}, routes: routes})
	all = append(all, both("exec without a command", routes, "exec", "--detach", "--cpu-cores", "4")...)
	all = append(all, both("exec cpu and gpu", routes, "exec", "--detach", "--cpu-cores", "4", "--gpu-count", "1", "--", "true")...)
	all = append(all, both("exec bad timeout", routes, "exec", "--detach", "--cpu-cores", "4", "--timeout", "0", "--", "true")...)
	all = append(all, both("exec from a template", routes, "exec", "--detach", "--name", "train", "--template", "my-trainer", "--registry", "ghcr", "--idempotency-key", "retry-1", "--no-retain-logs", "--preemption-retries", "2", "--", "python", "train.py")...)
	all = append(all, both("exec on gpus", routes, "exec", "--detach", "--name", "train", "--gpu-type", "mi325", "--image", "python:3.12", "--volume", "datasets:/data", "--secret", "K=v", "--workdir", "/data", "--", "python", "train.py")...)

	all = append(all, both("job cancel", routes, "job", "cancel", "jb7xk2mq9z")...)
	all = append(all, both("job delete", routes, "runjob", "delete", "jb7xk2mq9z", "--yes")...)
	all = append(all, both("job logs follow json", routes, "job", "logs", "jb7xk2mq9z", "--follow")...)
	all = append(all, both("job logs missing attempt", routes, "job", "logs", "jb7xk2mq9z", "--attempt", "7")...)

	queued := with(routes, "GET /run-jobs/jb7xk2mq9z", ok(runJobQueued))
	all = append(all, both("job get waiting at a discount", queued, "job", "get", "jb7xk2mq9z")...)

	all = append(all, both("templates create", routes, "templates", "create", "trainer", "--image", "ghcr.io/acme/trainer:1", "--gpu-count", "1", "--env", "EPOCHS=3", "--visibility", "organization", "--tag", "AMD", "--tag", "CUDA 12.8", "--port", "8080", "--description", "")...)
	all = append(all, both("templates create from official", routes, "templates", "create", "trainer", "--template", "pytorch:2.3.1", "--disk", "100", "--volume", "datasets:/data", "--registry", "ghcr")...)
	all = append(all, both("templates create without an image", routes, "templates", "create", "trainer")...)
	all = append(all, both("templates create both template and image", routes, "templates", "create", "trainer", "--template", "pytorch", "--image", "x:1")...)
	all = append(all, both("templates update", routes, "templates", "update", "my-trainer", "--name", "renamed", "--command", "", "--compute-type", "cpu", "--secret", "A=b")...)
	all = append(all, both("templates update nothing", routes, "templates", "update", "my-trainer")...)
	all = append(all, both("templates delete", routes, "templates", "delete", "my-trainer", "--yes")...)
	all = append(all, both("templates show missing", routes, "templates", "show", "nope")...)

	all = append(all, both("ssh-keys add without a key", routes, "ssh-keys", "add")...)
	all = append(all, both("ssh-keys add unreadable file", routes, "ssh-keys", "add", "/nonexistent/id.pub")...)
	all = append(all, both("ssh-keys delete", routes, "ssh-keys", "delete", "Laptop", "--yes")...)
	all = append(all, both("ssh-keys delete unknown", routes, "ssh-keys", "delete", "nope", "--yes")...)

	for _, s := range both("registry-keys add", routes, "registry-keys", "add", "ghcr2", "--server", "ghcr.io", "--username", "bot", "--password-stdin") {
		s.stdin = "s3cret\r\n"
		all = append(all, s)
	}

	for _, s := range both("registry-keys add empty stdin", routes, "registry-keys", "add", "ghcr2", "--server", "ghcr.io", "--username", "bot", "--password-stdin") {
		s.stdin = "\n"
		all = append(all, s)
	}

	forbidden := with(routes, "POST /registry-credentials", route{status: 403, body: `{"error":{"code":"forbidden","message":"Missing scope."}}`})

	for _, s := range both("registry-keys add forbidden", forbidden, "registry-keys", "add", "ghcr2", "--server", "ghcr.io", "--username", "bot", "--password-stdin") {
		s.stdin = "s3cret"
		all = append(all, s)
	}

	all = append(all, both("registry-keys add without stdin", routes, "registry-keys", "add", "ghcr2", "--server", "ghcr.io", "--username", "bot")...)
	all = append(all, both("registry-keys update", routes, "registry-keys", "update", "ghcr", "--name", "ghcr2", "--server", "ghcr.io")...)
	all = append(all, both("registry-keys update nothing", routes, "registry-keys", "update", "ghcr")...)
	all = append(all, both("registry-keys delete", routes, "registry-keys", "delete", "4c5d6e7f-8091-4a2b-8c3d-4e5f60718293", "--yes")...)

	metered := with(routes, "GET /billing/ledger", ok(ledgerMetered))
	all = append(all, both("billing history metered", metered, "billing", "history")...)

	return all
}
