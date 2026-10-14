import {
  afterEach, describe, expect, it, vi,
} from 'vitest';
import { CloudClient, errorFromStatus, type CreateRunJobBody } from '../src/clients/cloud';
import { remoteExitCode } from '../src/commands/exec';
import { newContent, nextLogCursor } from '../src/commands/instance/logs';
import { RetryableError } from '../src/error';
import { runJobDtoSchema, type RunJobDto } from '../src/types/job';

const JOB: RunJobDto = {
  id: '4d3af7b0-3498-48b3-ab3f-af81ef246cd6',
  shortId: 'vgjggsynrr',
  name: 'capture',
  state: 'running',
  computeType: 'cpu',
  computeSku: 'cpu.general.2vcpu',
  gpuCount: 0,
  resources: { gpuModel: null, gpuMemoryGiB: null, cpuCores: 2, memoryGiB: 8 },
  ephemeralDiskGiB: 20,
  command: ['/bin/sh', '-c', 'echo one; echo two'],
  workingDirectory: '/workspace',
  environment: [],
  timeoutSeconds: 120,
  preemptionRetries: 0,
  attemptNumber: 1,
  attempts: [{
    number: 1, state: 'running', scheduledAt: '2026-09-04T12:29:41.000Z', startedAt: '2026-09-04T12:29:43.109Z', finishedAt: null, exitCode: null, containerRunId: null,
  }],
  retainLogs: true,
  logsComplete: false,
  logsTruncated: false,
  exitCode: null,
  statusReason: null,
  statusMessage: null,
  scheduledAt: '2026-09-04T12:29:41.000Z',
  startedAt: '2026-09-04T12:29:43.109Z',
  finishedAt: null,
  costMicrosPerHour: '82740',
  image: { source: 'custom', reference: 'docker.io/library/alpine@sha256:abc' },
  networkVolumeMounts: [],
  createdAt: '2026-09-04T12:29:39.833Z',
  updatedAt: '2026-09-04T12:29:43.109Z',
};

afterEach(() => vi.unstubAllGlobals());

describe('job client', () => {
  it('treats API server failures as reconnectable', () => {
    expect(errorFromStatus(500, '')).toBeInstanceOf(RetryableError);
    expect(errorFromStatus(503, '')).toBeInstanceOf(RetryableError);
  });

  it('accepts the customer job response shape', () => {
    expect(runJobDtoSchema.parse(JOB).command).toEqual(['/bin/sh', '-c', 'echo one; echo two']);
  });

  it('submits the exact argv and idempotency key', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify(JOB), {
      status: 202,
      headers: { 'content-type': 'application/json' },
    }));

    vi.stubGlobal('fetch', fetchMock);

    const body: CreateRunJobBody = {
      name: 'capture',
      computeSku: 'cpu.general.2vcpu',
      command: ['/bin/sh', '-c', 'echo one; echo two'],
    };

    await new CloudClient('https://api.example.test/', 'secret').createJob(body, 'retry-key');

    const [url, request] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe('https://api.example.test/run-jobs/');
    expect(request.method).toBe('POST');
    expect(JSON.parse(String(request.body))).toEqual(body);
    expect(new Headers(request.headers).get('Idempotency-Key')).toBe('retry-key');
  });

  it('returns the remote process exit code', () => {
    expect(remoteExitCode({ ...JOB, state: 'succeeded', exitCode: 0 })).toBe(0);
    expect(remoteExitCode({ ...JOB, state: 'failed', exitCode: 37 })).toBe(37);
    expect(remoteExitCode({ ...JOB, state: 'timed_out', exitCode: null })).toBe(124);
    expect(remoteExitCode({ ...JOB, state: 'cancelled', exitCode: null })).toBe(130);
  });

  it('does not replay a retained tail after a temporary empty live-log response', () => {
    const previous = 'progress-0\nprogress-1\n';
    const preserved = nextLogCursor(previous, '', false);

    expect(newContent(preserved, previous)).toBe('');
  });

  it('requests retained logs by short id and tail size', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      logs: 'one\ntwo\n', complete: true, truncated: false,
    }), { status: 200, headers: { 'content-type': 'application/json' } }));

    vi.stubGlobal('fetch', fetchMock);

    const logs = await new CloudClient('https://api.example.test', 'secret').jobLogs('vgjggsynrr', 25);

    expect(logs).toEqual({ logs: 'one\ntwo\n', complete: true, truncated: false });
    expect(fetchMock.mock.calls[0]?.[0]).toBe('https://api.example.test/run-jobs/vgjggsynrr/logs?section=tail&tailLines=25');
  });

  it('reads an earlier attempt\'s output by its run id', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response(JSON.stringify({
      status: 'available', text: 'first attempt\n', omittedBytes: 0,
    }), { status: 200, headers: { 'content-type': 'application/json' } }));

    vi.stubGlobal('fetch', fetchMock);

    const history = await new CloudClient('https://api.example.test', 'secret').jobLogHistory(JOB.id, 'a'.repeat(64));

    expect(history.text).toBe('first attempt\n');
    expect(fetchMock.mock.calls[0]?.[0]).toBe(`https://api.example.test/run-jobs/${JOB.id}/logs/history?containerRunId=${'a'.repeat(64)}&section=tail`);
  });
});
