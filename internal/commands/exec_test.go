package commands

import (
	"encoding/json"
	"testing"

	"featherless.ai/featherless-cloud/internal/api"
)

// jobJSON is the TypeScript tests' JOB fixture.
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

func jobFixture(t *testing.T, state api.RunJobState, exitCode *int) api.RunJob {
	t.Helper()
	var job api.RunJob

	if err := json.Unmarshal([]byte(jobJSON), &job); err != nil {
		t.Fatal(err)
	}

	job.State = state
	job.ExitCode = exitCode

	return job
}

func TestJobClientReturnsTheRemoteProcessExitCode(t *testing.T) {
	cases := []struct {
		state    api.RunJobState
		exitCode *int
		want     int
	}{
		{api.JobSucceeded, new(0), 0},
		{api.JobFailed, new(37), 37},
		{api.JobTimedOut, nil, 124},
		{api.JobCancelled, nil, 130},
	}

	for _, c := range cases {
		job := jobFixture(t, c.state, c.exitCode)

		if got := remoteExitCode(job); got != c.want {
			t.Errorf("%s: remoteExitCode = %d, want %d", c.state, got, c.want)
		}
	}
}
