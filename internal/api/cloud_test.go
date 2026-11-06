package api

import (
	"context"
	"net/http"
	"reflect"
	"testing"

	"featherless.ai/featherless-cloud/internal/errs"
	"featherless.ai/featherless-cloud/internal/version"
)

func TestErrorFromStatusFallsBackToTheDefaultMessages(t *testing.T) {
	cases := []struct {
		status int
		body   string
		kind   errs.Kind
		want   string
	}{
		{401, "", errs.KindUnauthorized, "Unauthorized — your API key may have been revoked. Run featherless-cloud login."},
		{403, "", errs.KindForbidden, "Forbidden: the API key lacks the required scope."},
		{404, "", errs.KindNotFound, "Not found"},
		{409, "", errs.KindConflict, "Conflict"},
		{422, "", errs.KindCLI, "The request was rejected."},
		{429, "", errs.KindRateLimit, "Rate limited"},
		{503, "", errs.KindServiceUnavailable, "API error 503: 503"},
		{502, "  upstream timeout \n", errs.KindServiceUnavailable, "API error 502: upstream timeout"},
		{400, "", errs.KindCLI, "API error 400: 400"},
		{400, `{"error":{"code":"bad_request","message":"Bad page."}}`, errs.KindCLI, "API error 400: Bad page."},
	}

	for _, c := range cases {
		err := ErrorFromStatus(c.status, c.body)

		if !errs.IsKind(err, c.kind) {
			t.Errorf("%d %q: kind of %#v, want %d", c.status, c.body, err, c.kind)
		}

		if err == nil || err.Error() != c.want {
			t.Errorf("%d %q: message %v, want %q", c.status, c.body, err, c.want)
		}
	}

	if code := errs.ConflictCode(ErrorFromStatus(409, "")); code != "conflict" {
		t.Errorf("409 default code = %q, want conflict", code)
	}
}

func TestErrorFromStatusPointsInsufficientCreditAtTheConsole(t *testing.T) {
	t.Setenv("FEATHERLESS_CLOUD_CONSOLE_URL", "https://console.example.test")

	err := ErrorFromStatus(409, `{"error":{"code":"insufficient_credit","message":"Not enough credit."}}`)

	assertMessage(t, err, "Not enough credit. Run featherless-cloud billing settings, or add credit at https://console.example.test/billing.")
}

func TestCallsEachEndpointWithItsMethodPathAndQuery(t *testing.T) {
	cases := []struct {
		want string
		call func(ctx context.Context, client *Client) error
	}{
		{"GET /me", func(ctx context.Context, client *Client) error {
			_, err := client.GetMe(ctx)
			return err
		}},
		{"GET /instances?page=2&limit=50", func(ctx context.Context, client *Client) error {
			_, err := client.ListInstances(ctx, 2, 50)
			return err
		}},
		{"GET /instances/rcsrvch012", func(ctx context.Context, client *Client) error {
			_, err := client.GetInstance(ctx, "rcsrvch012")
			return err
		}},
		{"GET /instances/a%20b%2Fc", func(ctx context.Context, client *Client) error {
			_, err := client.GetInstance(ctx, "a b/c")
			return err
		}},
		{"POST /instances/", func(ctx context.Context, client *Client) error {
			_, err := client.CreateInstance(ctx, CreateInstanceBody{Name: "research"}, "key-1")
			return err
		}},
		{"PATCH /instances/i1", func(ctx context.Context, client *Client) error {
			_, err := client.PatchInstance(ctx, "i1", UpdateInstanceBody{Name: new("renamed")})
			return err
		}},
		{"POST /instances/i1/restart", func(ctx context.Context, client *Client) error {
			_, err := client.InstanceAction(ctx, "i1", ActionRestart)
			return err
		}},
		{"GET /instances/i1/history", func(ctx context.Context, client *Client) error {
			_, err := client.InstanceHistory(ctx, "i1")
			return err
		}},
		{"GET /instances/i1/logs?section=tail&tailLines=100", func(ctx context.Context, client *Client) error {
			_, err := client.InstanceLogs(ctx, "i1", 100, SectionTail)
			return err
		}},
		{"GET /instances/i1/logs?section=head", func(ctx context.Context, client *Client) error {
			_, err := client.InstanceLogs(ctx, "i1", 0, SectionHead)
			return err
		}},
		{"POST /instances/i1/terminal/sessions", func(ctx context.Context, client *Client) error {
			_, err := client.CreateTerminalSession(ctx, "i1", 80, 24, TerminalTarget{Tmux: "default"})
			return err
		}},
		{"GET /templates/", func(ctx context.Context, client *Client) error {
			_, err := client.Templates(ctx)
			return err
		}},
		{"GET /organization", func(ctx context.Context, client *Client) error {
			_, err := client.Organization(ctx)
			return err
		}},
		{"GET /volumes/", func(ctx context.Context, client *Client) error {
			_, err := client.ListVolumes(ctx)
			return err
		}},
		{"POST /volumes/", func(ctx context.Context, client *Client) error {
			_, err := client.CreateVolume(ctx, CreateVolumeBody{Name: "datasets", CapacityGiB: 100}, "key-1")
			return err
		}},
		{"GET /volumes/v1", func(ctx context.Context, client *Client) error {
			_, err := client.GetVolume(ctx, "v1")
			return err
		}},
		{"PATCH /volumes/v1", func(ctx context.Context, client *Client) error {
			_, err := client.PatchVolume(ctx, "v1", UpdateVolumeBody{CapacityGiB: new(200)})
			return err
		}},
		{"POST /volumes/v1/files/session", func(ctx context.Context, client *Client) error {
			_, err := client.CreateFileSession(ctx, "v1")
			return err
		}},
		{"DELETE /volumes/v1", func(ctx context.Context, client *Client) error {
			_, err := client.DeleteVolume(ctx, "v1", false)
			return err
		}},
		{"DELETE /volumes/v1?detach=true", func(ctx context.Context, client *Client) error {
			_, err := client.DeleteVolume(ctx, "v1", true)
			return err
		}},
		{"GET /run-jobs?page=1&limit=100", func(ctx context.Context, client *Client) error {
			_, err := client.ListJobs(ctx, 1, 100)
			return err
		}},
		{"GET /run-jobs/j1", func(ctx context.Context, client *Client) error {
			_, err := client.GetJob(ctx, "j1")
			return err
		}},
		{"GET /run-jobs/j1/logs?section=head", func(ctx context.Context, client *Client) error {
			_, err := client.JobLogs(ctx, "j1", 0, SectionHead)
			return err
		}},
		{"POST /run-jobs/j1/cancel", func(ctx context.Context, client *Client) error {
			_, err := client.CancelJob(ctx, "j1")
			return err
		}},
		{"DELETE /run-jobs/j1", func(ctx context.Context, client *Client) error {
			_, err := client.DeleteJob(ctx, "j1")
			return err
		}},
		{"GET /me/ssh-keys", func(ctx context.Context, client *Client) error {
			_, err := client.SSHKeys(ctx)
			return err
		}},
		{"POST /me/ssh-keys", func(ctx context.Context, client *Client) error {
			_, err := client.AddSSHKey(ctx, AddSSHKeyBody{Name: "laptop", PublicKey: "ssh-ed25519 AAAA"})
			return err
		}},
		{"DELETE /me/ssh-keys/k1", func(ctx context.Context, client *Client) error {
			_, err := client.DeleteSSHKey(ctx, "k1")
			return err
		}},
		{"GET /registry-credentials", func(ctx context.Context, client *Client) error {
			_, err := client.RegistryCredentials(ctx)
			return err
		}},
		{"POST /registry-credentials", func(ctx context.Context, client *Client) error {
			body := RegistryCredentialBody{Name: "hub", Registry: "docker.io", Username: "ada", Secret: "s3cret"}
			_, err := client.AddRegistryCredential(ctx, body)
			return err
		}},
		{"PATCH /registry-credentials/r1", func(ctx context.Context, client *Client) error {
			_, err := client.UpdateRegistryCredential(ctx, "r1", UpdateRegistryCredentialBody{Username: new("grace")})
			return err
		}},
		{"DELETE /registry-credentials/r1", func(ctx context.Context, client *Client) error {
			_, err := client.DeleteRegistryCredential(ctx, "r1")
			return err
		}},
		{"GET /billing", func(ctx context.Context, client *Client) error {
			_, err := client.BillingSummary(ctx)
			return err
		}},
		{"GET /billing/ledger", func(ctx context.Context, client *Client) error {
			_, err := client.BillingLedger(ctx)
			return err
		}},
	}

	for _, c := range cases {
		server := newFakeAPI(t, respondJSON(200, "{}"))

		// The empty body fails most schemas; only the request matters here.
		_ = c.call(context.Background(), NewClient(server.URL, "test-key"))

		requests := server.Requests()

		if len(requests) != 1 {
			t.Errorf("%s: %d requests", c.want, len(requests))
			continue
		}

		got := requests[0].Method + " " + requests[0].URI

		if got != c.want {
			t.Errorf("requested %q, want %q", got, c.want)
		}
	}
}

func TestSendsTheKeyAndUserAgentAndMarksJSONBodies(t *testing.T) {
	server := newFakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			respondJSON(200, "[]")(w, r)
			return
		}

		respondJSON(202, volumeJSON)(w, r)
	})

	client := NewClient(server.URL, "test-key")
	ctx := context.Background()

	if _, err := client.ListVolumes(ctx); err != nil {
		t.Fatal(err)
	}

	if _, err := client.CreateVolume(ctx, CreateVolumeBody{Name: "datasets", CapacityGiB: 100}, "key-1"); err != nil {
		t.Fatal(err)
	}

	requests := server.Requests()

	if len(requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(requests))
	}

	for _, request := range requests {
		if got := request.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Errorf("%s Authorization = %q", request.Method, got)
		}

		if got := request.Header.Get("User-Agent"); got != "featherless-cloud/"+version.Version {
			t.Errorf("%s User-Agent = %q", request.Method, got)
		}
	}

	if got := requests[0].Header.Get("Content-Type"); got != "" {
		t.Errorf("GET Content-Type = %q, want none", got)
	}

	if got := requests[1].Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("POST Content-Type = %q", got)
	}

	if got := requests[1].Header.Get("Idempotency-Key"); got != "key-1" {
		t.Errorf("Idempotency-Key = %q", got)
	}

	assertSameJSON(t, requests[1].Body, `{"name":"datasets","capacityGiB":100}`)
}

func TestReportsAnUnreachableAPIAsRetryable(t *testing.T) {
	server := newFakeAPI(t, respondJSON(200, "{}"))
	base := server.URL
	server.Close()

	_, err := NewClient(base, "test-key").GetMe(context.Background())

	if !errs.IsKind(err, errs.KindRetryable) {
		t.Errorf("error %#v is not retryable", err)
	}

	assertMessage(t, err, "Could not connect to "+base)
}

func TestMapsAFailedResponseThroughErrorFromStatus(t *testing.T) {
	server := newFakeAPI(t, respondJSON(404, `{"error":{"code":"not_found","message":"No such instance"}}`))

	_, err := NewClient(server.URL, "test-key").GetInstance(context.Background(), "missing")

	assertKind(t, err, errs.KindNotFound)
	assertMessage(t, err, "No such instance")
}

func TestReturnsTheStatusOfADelete(t *testing.T) {
	server := newFakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	})

	status, err := NewClient(server.URL, "test-key").DeleteVolume(context.Background(), "v1", true)

	if err != nil {
		t.Fatal(err)
	}

	if status != http.StatusAccepted {
		t.Errorf("status = %d, want 202", status)
	}
}

func TestUnwrapsDataEnvelopes(t *testing.T) {
	server := newFakeAPI(t, respondJSON(200, `{"data":[{"id":"k1","name":"laptop","fingerprint":"SHA256:abc","createdAt":"2026-01-01T00:00:00Z"}]}`))

	keys, err := NewClient(server.URL, "test-key").SSHKeys(context.Background())

	if err != nil {
		t.Fatal(err)
	}

	want := []SSHKey{{ID: "k1", Name: "laptop", Fingerprint: "SHA256:abc", CreatedAt: "2026-01-01T00:00:00Z"}}

	if !reflect.DeepEqual(keys, want) {
		t.Errorf("keys = %#v, want %#v", keys, want)
	}
}

func TestOpensATerminalSessionForANewOrAnAttachedTmuxSession(t *testing.T) {
	server := newFakeAPI(t, respondJSON(200, `{"token":"t1","wsPath":"/ws","tmuxSession":{"id":"tmux-1","name":"default","windows":1,"attached":0}}`))
	client := NewClient(server.URL, "test-key")
	ctx := context.Background()

	session, err := client.CreateTerminalSession(ctx, "i1", 120, 40, TerminalTarget{Tmux: "default"})

	if err != nil {
		t.Fatal(err)
	}

	if session.Token != "t1" || session.WSPath != "/ws" || session.TmuxSession == nil || session.TmuxSession.ID != "tmux-1" {
		t.Errorf("session = %#v", session)
	}

	if _, err := client.CreateTerminalSession(ctx, "i1", 120, 40, TerminalTarget{TmuxSessionID: "tmux-1"}); err != nil {
		t.Fatal(err)
	}

	requests := server.Requests()

	if len(requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(requests))
	}

	assertSameJSON(t, requests[0].Body, `{"columns":120,"rows":40,"tmux":"default"}`)
	assertSameJSON(t, requests[1].Body, `{"columns":120,"rows":40,"tmuxSessionId":"tmux-1"}`)
}

const volumeJSON = `{
	"id": "11111111-2222-4333-8444-555555555555",
	"name": "datasets",
	"capacityGiB": 100,
	"state": "provisioning",
	"attachments": [],
	"pricing": {"unitPriceMicrosPerGiBMonth": "70000", "estimatedMicrosPerMonth": "7000000", "billingActive": false},
	"lastError": null,
	"createdAt": "2026-01-01T00:00:00Z",
	"updatedAt": "2026-01-01T00:00:00Z"
}`
