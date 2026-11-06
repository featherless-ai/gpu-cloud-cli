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

it('saves, changes and deletes templates by id', async () => {
  const saved = {
    id: '33333333-3333-4333-8333-333333333331',
    name: 'trainer',
    description: '',
    imageRepository: null,
    enabled: true,
    computeTypes: ['gpu'],
    version: {
      id: '33333333-3333-4333-8333-333333333331', version: '', tag: null, state: 'published', image: 'ghcr.io/acme/trainer:1', startupCommand: '',
    },
    shortId: 'k3m9x2bq7t',
    slug: 'trainer',
    official: false,
    visibility: 'private',
    owner: { id: 'u1', name: 'Ada' },
    canEdit: true,
    image: 'ghcr.io/acme/trainer:1',
    imageSource: 'custom',
    registryCredentialId: null,
    computeSku: null,
    ephemeralDiskGiB: null,
    networkVolumeMounts: [],
    command: [],
    environment: [],
    secretNames: [],
    publicPorts: [],
    autoTmux: null,
  };

  const fetchMock = vi.fn().mockImplementation(async (_url: string, init: RequestInit) => (init.method === 'DELETE'
    ? new Response(null, { status: 204 })
    : new Response(JSON.stringify(saved), { headers: { 'content-type': 'application/json' } })));

  vi.stubGlobal('fetch', fetchMock);
  const client = new CloudClient('https://api.example.test', 'test-key');

  await client.createTemplate({ name: 'trainer', containerImage: 'ghcr.io/acme/trainer:1' });
  expect((await client.template('k3m9x2bq7t')).tags).toEqual([]);
  await client.updateTemplate(saved.id, { visibility: 'organization', tags: ['AMD', 'ROCm 7.14'] });
  await client.deleteTemplate(saved.id);

  expect(fetchMock.mock.calls.map(call => [(call[1] as RequestInit).method, call[0]])).toEqual([
    ['POST', 'https://api.example.test/templates/'],
    ['GET', 'https://api.example.test/templates/k3m9x2bq7t'],
    ['PATCH', `https://api.example.test/templates/${saved.id}`],
    ['DELETE', `https://api.example.test/templates/${saved.id}`],
  ]);

  expect(JSON.parse((fetchMock.mock.calls[2]![1] as RequestInit).body as string)).toEqual({ visibility: 'organization', tags: ['AMD', 'ROCm 7.14'] });
});
