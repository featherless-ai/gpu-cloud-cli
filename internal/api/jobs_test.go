package api

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"featherless.ai/featherless-cloud/internal/errs"
)

const jobID = "4d3af7b0-3498-48b3-ab3f-af81ef246cd6"

const jobJSON = `{
	"id": "4d3af7b0-3498-48b3-ab3f-af81ef246cd6",
	"shortId": "vgjggsynrr",
	"name": "capture",
	"state": "running",
	"computeType": "cpu",
	"computeSku": "cpu.general.2vcpu",
	"gpuCount": 0,
	"resources": {"gpuModel": null, "gpuMemoryGiB": null, "cpuCores": 2, "memoryGiB": 8},
	"ephemeralDiskGiB": 20,
	"command": ["/bin/sh", "-c", "echo one; echo two"],
	"workingDirectory": "/workspace",
	"environment": [],
	"timeoutSeconds": 120,
	"preemptionRetries": 0,
	"attemptNumber": 1,
	"attempts": [{
		"number": 1, "state": "running", "scheduledAt": "2026-09-04T12:29:41.000Z", "startedAt": "2026-09-04T12:29:43.109Z", "finishedAt": null, "exitCode": null, "containerRunId": null
	}],
	"retainLogs": true,
	"logsComplete": false,
	"logsTruncated": false,
	"exitCode": null,
	"statusReason": null,
	"statusMessage": null,
	"scheduledAt": "2026-09-04T12:29:41.000Z",
	"startedAt": "2026-09-04T12:29:43.109Z",
	"finishedAt": null,
	"costMicrosPerHour": "82740",
	"image": {"source": "custom", "reference": "docker.io/library/alpine@sha256:abc"},
	"networkVolumeMounts": [],
	"createdAt": "2026-09-04T12:29:39.833Z",
	"updatedAt": "2026-09-04T12:29:43.109Z"
}`

func TestJobClientTreatsAPIServerFailuresAsReconnectable(t *testing.T) {
	if err := ErrorFromStatus(500, ""); !errs.IsRetryable(err) {
		t.Errorf("500: %#v is not retryable", err)
	}

	if err := ErrorFromStatus(503, ""); !errs.IsRetryable(err) {
		t.Errorf("503: %#v is not retryable", err)
	}
}

func TestJobClientAcceptsTheCustomerJobResponseShape(t *testing.T) {
	job, err := parse[RunJob]([]byte(jobJSON))

	if err != nil {
		t.Fatal(err)
	}

	want := []string{"/bin/sh", "-c", "echo one; echo two"}

	if !reflect.DeepEqual(job.Command, want) {
		t.Errorf("command = %#v, want %#v", job.Command, want)
	}
}

func TestJobClientSubmitsTheExactArgvAndIdempotencyKey(t *testing.T) {
	server := newFakeAPI(t, respondJSON(202, jobJSON))

	body := CreateRunJobBody{
		Name:       "capture",
		ComputeSKU: new("cpu.general.2vcpu"),
		Command:    []string{"/bin/sh", "-c", "echo one; echo two"},
	}

	if _, err := NewClient(server.URL+"/", "secret").CreateJob(context.Background(), body, "retry-key"); err != nil {
		t.Fatal(err)
	}

	requests := server.Requests()

	if len(requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(requests))
	}

	request := requests[0]

	if request.URI != "/run-jobs/" {
		t.Errorf("uri = %q", request.URI)
	}

	if request.Method != "POST" {
		t.Errorf("method = %q", request.Method)
	}

	assertSameJSON(t, request.Body, `{"name":"capture","computeSku":"cpu.general.2vcpu","command":["/bin/sh","-c","echo one; echo two"]}`)

	if key := request.Header.Get("Idempotency-Key"); key != "retry-key" {
		t.Errorf("Idempotency-Key = %q", key)
	}
}

func TestJobClientRequestsRetainedLogsByShortIDAndTailSize(t *testing.T) {
	server := newFakeAPI(t, respondJSON(200, `{"logs":"one\ntwo\n","complete":true,"truncated":false}`))

	logs, err := NewClient(server.URL, "secret").JobLogs(context.Background(), "vgjggsynrr", 25, SectionTail)

	if err != nil {
		t.Fatal(err)
	}

	want := RunJobLogs{Logs: "one\ntwo\n", Complete: true, Truncated: false}

	if logs != want {
		t.Errorf("logs = %#v, want %#v", logs, want)
	}

	requests := server.Requests()

	if len(requests) != 1 || requests[0].URI != "/run-jobs/vgjggsynrr/logs?section=tail&tailLines=25" {
		t.Errorf("requests = %#v", requests)
	}
}

func TestJobClientReadsAnEarlierAttemptsOutputByItsRunID(t *testing.T) {
	server := newFakeAPI(t, respondJSON(200, `{"status":"available","text":"first attempt\n","omittedBytes":0}`))
	runID := strings.Repeat("a", 64)

	history, err := NewClient(server.URL, "secret").JobLogHistory(context.Background(), jobID, runID, SectionTail)

	if err != nil {
		t.Fatal(err)
	}

	if history.Text != "first attempt\n" {
		t.Errorf("text = %q", history.Text)
	}

	requests := server.Requests()
	want := "/run-jobs/" + jobID + "/logs/history?containerRunId=" + runID + "&section=tail"

	if len(requests) != 1 || requests[0].URI != want {
		t.Errorf("requests = %#v, want %q", requests, want)
	}
}
