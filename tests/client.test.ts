import {
  afterEach, describe, expect, it, vi,
} from 'vitest';
import { CloudClient, errorFromStatus } from '../src/clients/cloud';
import {
  CliError, ConflictError, ForbiddenError, NotFoundError, RateLimitError, UnauthorizedError,
} from '../src/error';

describe('errorFromStatus', () => {
  it('maps 401 with the envelope message', () => {
    const err = errorFromStatus(401, JSON.stringify({ error: { code: 'unauthenticated', message: 'Key revoked' } }));
    expect(err).toBeInstanceOf(UnauthorizedError);
    expect(err.message).toBe('Key revoked');
  });

  it('maps 403 to ForbiddenError', () => {
    const err = errorFromStatus(403, JSON.stringify({ error: { code: 'forbidden', message: 'Missing scope' } }));
    expect(err).toBeInstanceOf(ForbiddenError);
    expect(err.message).toBe('Missing scope');
  });

  it('maps 429 to RateLimitError', () => {
    expect(errorFromStatus(429, '')).toBeInstanceOf(RateLimitError);
  });

  it('maps a non-JSON 500 body to CliError with the status', () => {
    const err = errorFromStatus(500, 'gateway blew up');
    expect(err).toBeInstanceOf(CliError);
    expect(err.message).toBe('API error 500: gateway blew up');
  });

  it('maps 404 to NotFoundError', () => {
    const err = errorFromStatus(404, JSON.stringify({ error: { code: 'not_found', message: 'No such instance' } }));
    expect(err).toBeInstanceOf(NotFoundError);
    expect(err.message).toBe('No such instance');
  });

  it('maps 409 to ConflictError carrying the machine code', () => {
    const err = errorFromStatus(409, JSON.stringify({
      error: { code: 'network_volume_attached', message: 'Detach the volume first.' },
    }));

    expect(err).toBeInstanceOf(ConflictError);
    expect((err as ConflictError).code).toBe('network_volume_attached');
    expect(err.message).toBe('Detach the volume first.');
  });

  it('appends the credit pointer for insufficient_credit', () => {
    const err = errorFromStatus(409, JSON.stringify({
      error: { code: 'insufficient_credit', message: 'Not enough credit.' },
    })) as ConflictError;

    expect(err.code).toBe('insufficient_credit');
    expect(err.message).toContain('billing settings');
    expect(err.message).toContain('billing');
  });

  it('surfaces 422 field messages line by line', () => {
    const err = errorFromStatus(422, JSON.stringify({
      error: {
        code: 'validation_failed',
        message: 'The request was rejected.',
        fields: [
          { field: 'name', message: 'Name is required.' },
          { field: 'capacityGiB', message: 'Too small.' },
        ],
      },
    }));

    expect(err).toBeInstanceOf(CliError);
    expect(err.message).toBe('The request was rejected.\nname: Name is required.\ncapacityGiB: Too small.');
  });
});


afterEach(() => vi.unstubAllGlobals());

it('uses the selected workload price endpoint and defaults listings to instances', async () => {
  const fetchMock = vi.fn().mockImplementation(async () => new Response('[]', { headers: { 'content-type': 'application/json' } }));
  vi.stubGlobal('fetch', fetchMock);
  const client = new CloudClient('https://api.example.test', 'test-key');
  await client.pricing();
  await client.pricing('run-job');

  expect(fetchMock.mock.calls.map(call => call[0])).toEqual([
    'https://api.example.test/instances/pricing',
    'https://api.example.test/run-jobs/pricing',
  ]);
});
