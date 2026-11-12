package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"featherless.ai/featherless-cloud/internal/errs"
)

type recordedRequest struct {
	Method string
	URI    string
	Header http.Header
	Body   string
}

// fakeAPI records every request it receives and answers with respond.
type fakeAPI struct {
	*httptest.Server
	mu       sync.Mutex
	requests []recordedRequest
}

func newFakeAPI(t *testing.T, respond http.HandlerFunc) *fakeAPI {
	t.Helper()
	server := &fakeAPI{}

	server.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		request := recordedRequest{Method: r.Method, URI: r.URL.RequestURI(), Header: r.Header.Clone(), Body: string(body)}

		server.mu.Lock()
		server.requests = append(server.requests, request)
		server.mu.Unlock()

		respond(w, r)
	}))

	t.Cleanup(server.Close)

	return server
}

func (f *fakeAPI) Requests() []recordedRequest {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]recordedRequest(nil), f.requests...)
}

func respondJSON(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		io.WriteString(w, body)
	}
}

// assertSameJSON compares two JSON documents by value, like toEqual.
func assertSameJSON(t *testing.T, got, want string) {
	t.Helper()
	var gotValue, wantValue any

	if err := json.Unmarshal([]byte(got), &gotValue); err != nil {
		t.Fatalf("got invalid JSON %q: %v", got, err)
	}

	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatalf("want invalid JSON %q: %v", want, err)
	}

	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Errorf("JSON = %s, want %s", got, want)
	}
}

func assertKind(t *testing.T, err error, kind errs.Kind) {
	t.Helper()

	if !errs.IsKind(err, kind) {
		t.Errorf("error %v (%#v) is not kind %d", err, err, kind)
	}
}

func assertMessage(t *testing.T, err error, want string) {
	t.Helper()

	if err == nil {
		t.Fatalf("error = nil, want %q", want)
	}

	if err.Error() != want {
		t.Errorf("message = %q, want %q", err.Error(), want)
	}
}

func TestErrorFromStatusMaps401WithTheEnvelopeMessage(t *testing.T) {
	err := ErrorFromStatus(401, `{"error":{"code":"unauthenticated","message":"Key revoked"}}`)

	assertKind(t, err, errs.KindUnauthorized)
	assertMessage(t, err, "Key revoked")
}

func TestErrorFromStatusMaps403ToForbiddenError(t *testing.T) {
	err := ErrorFromStatus(403, `{"error":{"code":"forbidden","message":"Missing scope"}}`)

	assertKind(t, err, errs.KindForbidden)
	assertMessage(t, err, "Missing scope")
}

func TestErrorFromStatusMaps429ToRateLimitError(t *testing.T) {
	assertKind(t, ErrorFromStatus(429, ""), errs.KindRateLimit)
}

func TestErrorFromStatusMapsANonJSON500BodyToCliErrorWithTheStatus(t *testing.T) {
	err := ErrorFromStatus(500, "gateway blew up")

	if !errs.IsCLI(err) {
		t.Errorf("error %#v is not a CLI error", err)
	}

	assertMessage(t, err, "API error 500: gateway blew up")
}

func TestErrorFromStatusMaps404ToNotFoundError(t *testing.T) {
	err := ErrorFromStatus(404, `{"error":{"code":"not_found","message":"No such instance"}}`)

	assertKind(t, err, errs.KindNotFound)
	assertMessage(t, err, "No such instance")
}

func TestErrorFromStatusMaps409ToConflictErrorCarryingTheMachineCode(t *testing.T) {
	err := ErrorFromStatus(409, `{"error":{"code":"network_volume_attached","message":"Detach the volume first."}}`)

	assertKind(t, err, errs.KindConflict)

	if code := errs.ConflictCode(err); code != "network_volume_attached" {
		t.Errorf("code = %q", code)
	}

	assertMessage(t, err, "Detach the volume first.")
}

func TestErrorFromStatusAppendsTheCreditPointerForInsufficientCredit(t *testing.T) {
	err := ErrorFromStatus(409, `{"error":{"code":"insufficient_credit","message":"Not enough credit."}}`)

	if code := errs.ConflictCode(err); code != "insufficient_credit" {
		t.Errorf("code = %q", code)
	}

	if !strings.Contains(err.Error(), "billing settings") {
		t.Errorf("message %q lacks %q", err.Error(), "billing settings")
	}

	if !strings.Contains(err.Error(), "billing") {
		t.Errorf("message %q lacks %q", err.Error(), "billing")
	}
}

func TestErrorFromStatusSurfaces422FieldMessagesLineByLine(t *testing.T) {
	err := ErrorFromStatus(422, `{
		"error": {
			"code": "validation_failed",
			"message": "The request was rejected.",
			"fields": [
				{"field": "name", "message": "Name is required."},
				{"field": "capacityGiB", "message": "Too small."}
			]
		}
	}`)

	if !errs.IsCLI(err) {
		t.Errorf("error %#v is not a CLI error", err)
	}

	assertMessage(t, err, "The request was rejected.\nname: Name is required.\ncapacityGiB: Too small.")
}

func TestUsesTheSelectedWorkloadPriceEndpointAndDefaultsListingsToInstances(t *testing.T) {
	server := newFakeAPI(t, respondJSON(200, "[]"))
	client := NewClient(server.URL, "test-key")
	ctx := context.Background()

	if _, err := client.Pricing(ctx, WorkloadInstance); err != nil {
		t.Fatal(err)
	}

	if _, err := client.Pricing(ctx, WorkloadRunJob); err != nil {
		t.Fatal(err)
	}

	var uris []string

	for _, request := range server.Requests() {
		uris = append(uris, request.URI)
	}

	want := []string{"/instances/pricing", "/run-jobs/pricing"}

	if !reflect.DeepEqual(uris, want) {
		t.Errorf("requested %v, want %v", uris, want)
	}
}

const savedTemplateJSON = `{
	"id": "33333333-3333-4333-8333-333333333331",
	"name": "trainer",
	"description": "",
	"imageRepository": null,
	"enabled": true,
	"computeTypes": ["gpu"],
	"version": {
		"id": "33333333-3333-4333-8333-333333333331", "version": "", "tag": null, "state": "published", "image": "ghcr.io/acme/trainer:1", "startupCommand": ""
	},
	"shortId": "k3m9x2bq7t",
	"slug": "trainer",
	"official": false,
	"visibility": "private",
	"owner": {"id": "u1", "name": "Ada"},
	"canEdit": true,
	"image": "ghcr.io/acme/trainer:1",
	"imageSource": "custom",
	"registryCredentialId": null,
	"computeSku": null,
	"ephemeralDiskGiB": null,
	"networkVolumeMounts": [],
	"command": [],
	"environment": [],
	"secretNames": [],
	"publicPorts": [],
	"autoTmux": null
}`

func TestSavesChangesAndDeletesTemplatesByID(t *testing.T) {
	const savedID = "33333333-3333-4333-8333-333333333331"

	server := newFakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		respondJSON(200, savedTemplateJSON)(w, r)
	})

	client := NewClient(server.URL, "test-key")
	ctx := context.Background()

	if _, err := client.CreateTemplate(ctx, TemplateBody{Name: new("trainer"), ContainerImage: new("ghcr.io/acme/trainer:1")}); err != nil {
		t.Fatal(err)
	}

	template, err := client.Template(ctx, "k3m9x2bq7t")

	if err != nil {
		t.Fatal(err)
	}

	if template.Tags == nil || len(template.Tags) != 0 {
		t.Errorf("tags = %#v, want []", template.Tags)
	}

	update := TemplateBody{Visibility: new(VisibilityOrganization), Tags: []string{"AMD", "ROCm 7.14"}}

	if _, err := client.UpdateTemplate(ctx, savedID, update); err != nil {
		t.Fatal(err)
	}

	if _, err := client.DeleteTemplate(ctx, savedID); err != nil {
		t.Fatal(err)
	}

	requests := server.Requests()
	var calls [][2]string

	for _, request := range requests {
		calls = append(calls, [2]string{request.Method, request.URI})
	}

	want := [][2]string{
		{"POST", "/templates/"},
		{"GET", "/templates/k3m9x2bq7t"},
		{"PATCH", "/templates/" + savedID},
		{"DELETE", "/templates/" + savedID},
	}

	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}

	assertSameJSON(t, requests[2].Body, `{"visibility":"organization","tags":["AMD","ROCm 7.14"]}`)
}
