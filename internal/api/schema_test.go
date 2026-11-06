package api

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

const unexpectedResponse = "Unexpected response from the Featherless API."

const instanceJSON = `{
	"futureField": true,
	"name": "research",
	"id": "55555555-2222-4333-8444-555555555555",
	"shortId": "rcsrvch012",
	"computeLabel": "1x MI325X",
	"computeType": "gpu",
	"gpuCount": 1,
	"state": "running",
	"startupStage": null,
	"costMicrosPerHour": "1900000",
	"ephemeralDiskGiB": 30,
	"networkVolumeMounts": [{
		"volume": {"usage": {"usedBytes": 5, "at": "2026-01-01T00:00:00Z"}, "capacityGiB": 100, "name": "datasets", "id": "v1"},
		"mountPath": "/data"
	}],
	"publicPorts": [],
	"lastError": null,
	"createdAt": "2026-01-01T00:00:00Z"
}`

func TestKeepsSchemaOrderNullsAndPassthroughFieldsAndDropsUnknownOnes(t *testing.T) {
	instance, err := parse[Instance]([]byte(instanceJSON))

	if err != nil {
		t.Fatal(err)
	}

	encoded, err := json.Marshal(instance)

	if err != nil {
		t.Fatal(err)
	}

	want := `{"id":"55555555-2222-4333-8444-555555555555","shortId":"rcsrvch012","name":"research",` +
		`"computeLabel":"1x MI325X","computeType":"gpu","gpuCount":1,"state":"running","startupStage":null,` +
		`"costMicrosPerHour":"1900000","ephemeralDiskGiB":30,` +
		`"networkVolumeMounts":[{"mountPath":"/data","volume":{"id":"v1","name":"datasets","capacityGiB":100,` +
		`"usage":{"usedBytes":5,"at":"2026-01-01T00:00:00Z"}}}],` +
		`"publicPorts":[],"lastError":null,"createdAt":"2026-01-01T00:00:00Z"}`

	if string(encoded) != want {
		t.Errorf("encoded\n%s\nwant\n%s", encoded, want)
	}
}

func TestRejectsAResponseOutsideTheSchema(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(fields map[string]any)
	}{
		{"missing required field", func(fields map[string]any) { delete(fields, "name") }},
		{"null required field", func(fields map[string]any) { fields["name"] = nil }},
		{"missing nullable field", func(fields map[string]any) { delete(fields, "startupStage") }},
		{"null optional field", func(fields map[string]any) { fields["computeSku"] = nil }},
		{"unknown enum value", func(fields map[string]any) { fields["state"] = "paused" }},
		{"fractional integer", func(fields map[string]any) { fields["gpuCount"] = 1.5 }},
		{"wrong type", func(fields map[string]any) { fields["ephemeralDiskGiB"] = "30" }},
		{"incomplete nested object", func(fields map[string]any) {
			fields["publicPorts"] = []any{map[string]any{"port": 8000}}
		}},
	}

	for _, c := range cases {
		var fields map[string]any

		if err := json.Unmarshal([]byte(instanceJSON), &fields); err != nil {
			t.Fatal(err)
		}

		c.mutate(fields)
		data, err := json.Marshal(fields)

		if err != nil {
			t.Fatal(err)
		}

		_, err = parse[Instance](data)

		if err == nil || err.Error() != unexpectedResponse {
			t.Errorf("%s: error = %v, want %q", c.name, err, unexpectedResponse)
		}
	}
}

func TestRejectsMalformedIDsAndURLs(t *testing.T) {
	var job map[string]any

	if err := json.Unmarshal([]byte(jobJSON), &job); err != nil {
		t.Fatal(err)
	}

	job["id"] = "vgjggsynrr"
	data, err := json.Marshal(job)

	if err != nil {
		t.Fatal(err)
	}

	if _, err := parse[RunJob](data); err == nil || err.Error() != unexpectedResponse {
		t.Errorf("job id: error = %v, want %q", err, unexpectedResponse)
	}

	session := `{"token":"t","expiresAt":"2026-01-01T00:00:00Z","baseUrl":"not a url"}`

	if _, err := parse[FileSession]([]byte(session)); err == nil || err.Error() != unexpectedResponse {
		t.Errorf("file session: error = %v, want %q", err, unexpectedResponse)
	}
}

func TestReportsAnUnexpectedResponseFromAnEndpoint(t *testing.T) {
	server := newFakeAPI(t, respondJSON(200, `{"id":"i1"}`))

	_, err := NewClient(server.URL, "test-key").GetInstance(context.Background(), "i1")

	assertMessage(t, err, unexpectedResponse)
}

func TestFillsTemplateDefaults(t *testing.T) {
	template, err := parse[Template]([]byte(savedTemplateJSON))

	if err != nil {
		t.Fatal(err)
	}

	if template.Tags == nil || len(template.Tags) != 0 {
		t.Errorf("tags = %#v, want []", template.Tags)
	}

	if !reflect.DeepEqual(template.GPUVendors, []GPUVendor{VendorAMD, VendorNVIDIA}) {
		t.Errorf("gpuVendors = %#v", template.GPUVendors)
	}

	if template.Versions == nil || len(template.Versions) != 0 {
		t.Errorf("versions = %#v, want []", template.Versions)
	}

	var fields map[string]any

	if err := json.Unmarshal([]byte(savedTemplateJSON), &fields); err != nil {
		t.Fatal(err)
	}

	fields["tags"] = []string{"AMD"}
	fields["gpuVendors"] = []string{"amd"}
	data, err := json.Marshal(fields)

	if err != nil {
		t.Fatal(err)
	}

	tagged, err := parse[Template](data)

	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(tagged.Tags, []string{"AMD"}) || !reflect.DeepEqual(tagged.GPUVendors, []GPUVendor{VendorAMD}) {
		t.Errorf("tags = %#v, gpuVendors = %#v", tagged.Tags, tagged.GPUVendors)
	}
}

func TestSendsNullToClearAndAnEmptyListToEmpty(t *testing.T) {
	server := newFakeAPI(t, respondJSON(200, instanceJSON))

	body := UpdateInstanceBody{RegistryCredentialID: Null[string](), Command: []string{}}

	if _, err := NewClient(server.URL, "test-key").PatchInstance(context.Background(), "i1", body); err != nil {
		t.Fatal(err)
	}

	requests := server.Requests()

	if len(requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(requests))
	}

	assertSameJSON(t, requests[0].Body, `{"registryCredentialId":null,"command":[]}`)
}
