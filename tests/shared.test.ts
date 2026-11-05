import {
  afterEach, describe, expect, it, vi,
} from 'vitest';
import { Command } from 'commander';
import {
  assertInstanceName,
  findInstance,
  parsePorts,
  parseRepeatable,
  randomInstanceName,
  requireDeletion,
  resolveCompute,
  resolveImage,
  resolveInstanceId,
  resolveSshKeyRef,
  resolveTemplateRef,
  resolveVolume,
  templateReference,
  toEnvironment,
  validateMounts,
  checkTemplateVersion,
  splitTemplateVersion,
} from '../src/commands/shared';
import { newContent } from '../src/commands/instance/logs';
import { CliContext } from '../src/context';
import { CliError } from '../src/error';
import type { PriceRate } from '../src/types/pricing';
import type { Template } from '../src/types/templates';
import type { NetworkVolumeDto } from '../src/types/volume';
import type { RegistryCredential, SshKey } from '../src/types/instance';
import { parsePublicKey } from '../src/commands/ssh-keys/add';
import type { InstanceDto } from '../src/types/instance';
import type { CloudClient } from '../src/clients/cloud';

function ctx(mode: 'plain' | 'interactive' = 'plain'): CliContext {
  const root = new Command();
  root.option('--json');
  root.option('--non-interactive');

  if (mode === 'plain') {
    root.setOptionValue('nonInteractive', true);
  }

  return new CliContext(root);
}

function gpuRate(sku: string, gpuCount: number, gpuModel: string): PriceRate {
  return {
    sku,
    displayName: `${gpuCount}x ${gpuModel}`,
    resourceType: 'instance.gpu',
    unit: 'gpu_hour',
    gpuCount,
    unitPriceMicros: '1000000',
    compareAtUnitPriceMicros: null,
    currency: 'usd',
    effectiveAt: '2026-01-01T00:00:00Z',
    expiresAt: null,
    resources: { gpuModel, gpuMemoryGiB: 192, cpuCores: 16, memoryGiB: 256 },
  };
}

function cpuRate(sku: string, cpuCores: number): PriceRate {
  return {
    sku,
    displayName: `${cpuCores} vCPU`,
    resourceType: 'instance.cpu',
    unit: 'vcpu_hour',
    gpuCount: null,
    unitPriceMicros: '30000',
    compareAtUnitPriceMicros: null,
    currency: 'usd',
    effectiveAt: '2026-01-01T00:00:00Z',
    expiresAt: null,
    resources: { gpuModel: null, gpuMemoryGiB: null, cpuCores, memoryGiB: 32 },
  };
}

const PRICES: PriceRate[] = [
  gpuRate('gpu.mi325x.1x', 1, 'MI325X'),
  gpuRate('gpu.mi325x.2x', 2, 'MI325X'),
  gpuRate('gpu.h100.1x', 1, 'H100'),
  cpuRate('cpu.small', 4),
  cpuRate('cpu.large', 16),
];

function template(overrides: Partial<Template> & Pick<Template, 'id' | 'slug' | 'name' | 'visibility'>): Template {
  return {
    description: '',
    imageRepository: null,
    enabled: true,
    computeTypes: ['gpu'],
    version: {
      id: overrides.id, version: '', tag: null, state: 'published', image: 'ghcr.io/acme/app:1', startupCommand: '',
    },
    shortId: `${overrides.slug.replace(/[^a-z]/g, '').padEnd(10, 'b').slice(0, 10)}`,
    official: overrides.visibility === 'public',
    owner: overrides.visibility === 'public' ? null : { id: 'u1', name: 'Ada' },
    canEdit: overrides.visibility !== 'public',
    image: 'ghcr.io/acme/app:1',
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
    tags: [],
    versions: [],
    ...overrides,
  };
}

const PYTORCH = template({ id: '11111111-1111-4111-8111-111111111111', slug: 'rocm-pytorch', name: 'PyTorch (ROCm)', visibility: 'public' });
const MINE = template({ id: '33333333-3333-4333-8333-333333333331', slug: 'trainer', name: 'trainer', visibility: 'private' });
const SHARED = template({ id: '33333333-3333-4333-8333-333333333332', slug: 'trainer', name: 'trainer', visibility: 'organization' });
const SHARED_SERVE = template({ id: '33333333-3333-4333-8333-333333333333', slug: 'serve', name: 'serve', visibility: 'organization' });
const MY_PYTORCH = template({ id: '33333333-3333-4333-8333-333333333334', slug: 'rocm-pytorch', name: 'rocm-pytorch', visibility: 'private' });
const TEMPLATES = [PYTORCH, MINE, SHARED, SHARED_SERVE];

const CREDENTIALS: RegistryCredential[] = [{
  id: '22222222-2222-4222-8222-222222222222',
  name: 'Private Hub',
  registry: 'registry.example.com',
  username: 'robot',
  updatedAt: '2026-10-26T00:00:00.000Z',
}];

function volume(overrides: Partial<NetworkVolumeDto> = {}): NetworkVolumeDto {
  return {
    id: '33333333-3333-4333-8333-333333333333',
    name: 'data',
    capacityGiB: 100,
    state: 'available',
    attachments: [],
    pricing: {
      unitPriceMicrosPerGiBMonth: '50000',
      estimatedMicrosPerMonth: '5000000',
      billingActive: true,
    },
    lastError: null,
    createdAt: '2026-01-01T00:00:00Z',
    updatedAt: '2026-01-01T00:00:00Z',
    ...overrides,
  };
}

function instance(overrides: Partial<InstanceDto> = {}): InstanceDto {
  return {
    id: '55555555-5555-4555-8555-555555555555',
    shortId: 'tmxbnd1234',
    name: 'tmux-bundle-ubuntu',
    computeLabel: '1 x MI325X',
    computeType: 'gpu',
    gpuCount: 1,
    state: 'running',
    startupStage: null,
    costMicrosPerHour: '1900000',
    ephemeralDiskGiB: 30,
    createdAt: '2026-01-01T00:00:00Z',
    ...overrides,
  };
}

function clientWithPages(...pages: InstanceDto[]): CloudClient {
  return {
    listInstances: async ({ page }: { page: number }) => ({
      items: pages[page - 1] ? [pages[page - 1]!] : [],
      pagination: { page, pageSize: 1, totalItems: pages.length, totalPages: pages.length },
    }),
  } as unknown as CloudClient;
}

describe('parsePorts', () => {
  it('reads repeated ports, and none alone as no ports', () => {
    expect(parsePorts(['8000', '7860'])).toEqual([8000, 7860]);
    expect(parsePorts(['none'])).toEqual([]);
    expect(parsePorts(undefined)).toEqual([]);
  });

  it.each([['0'], ['65536'], ['8000.5'], ['http'], ['8000', '8000'], ['none', '8000']])('refuses %s', (...values) => {
    expect(() => parsePorts(values)).toThrow(CliError);
  });
});

describe('parseRepeatable', () => {
  it('splits on the first =', () => {
    expect(parseRepeatable(['A=1', 'B=x=y=z'], 'env')).toEqual([
      { name: 'A', value: '1' },
      { name: 'B', value: 'x=y=z' },
    ]);
  });

  it('rejects entries without a name', () => {
    expect(() => parseRepeatable(['novalue'], 'env')).toThrow('--env expects NAME=VALUE, got "novalue"');
    expect(() => parseRepeatable(['=x'], 'env')).toThrow(/expects NAME=VALUE/);
  });

  it('treats missing flags as no entries', () => {
    expect(parseRepeatable(undefined, 'secret')).toEqual([]);
  });
});

describe('toEnvironment', () => {
  it('passes valid unique names through', () => {
    const pairs = [{ name: 'HF_TOKEN', value: 'x' }, { name: '_A2', value: '' }];
    expect(toEnvironment(pairs)).toEqual(pairs);
  });

  it('rejects invalid characters with the console copy', () => {
    expect(() => toEnvironment([{ name: '9BAD', value: 'x' }])).toThrow('Variable names must be valid and unique.');
  });

  it('rejects duplicates with the console copy', () => {
    expect(() => toEnvironment([{ name: 'A', value: '1' }, { name: 'A', value: '2' }]))
      .toThrow('Variable names must be valid and unique.');
  });
});

describe('validateMounts', () => {
  it('accepts disjoint absolute paths', () => {
    expect(() => validateMounts([
      { networkVolumeId: 'a', mountPath: '/workspace' },
      { networkVolumeId: 'b', mountPath: '/data/models' },
    ])).not.toThrow();
  });

  it('rejects nested paths', () => {
    expect(() => validateMounts([
      { networkVolumeId: 'a', mountPath: '/workspace' },
      { networkVolumeId: 'b', mountPath: '/workspace/data' },
    ])).toThrow('Mount paths must not overlap.');
  });

  it('rejects // and trailing slashes', () => {
    expect(() => validateMounts([{ networkVolumeId: 'a', mountPath: '//workspace' }]))
      .toThrow('Use an absolute mount path such as /workspace.');

    expect(() => validateMounts([{ networkVolumeId: 'a', mountPath: '/workspace/' }]))
      .toThrow('Use an absolute mount path such as /workspace.');
  });

  it('rejects duplicate volume ids', () => {
    expect(() => validateMounts([
      { networkVolumeId: 'a', mountPath: '/one' },
      { networkVolumeId: 'a', mountPath: '/two' },
    ])).toThrow('Each network volume can be mounted only once.');
  });
});

describe('resolveCompute', () => {
  it('picks the unique gpu offer for type and count', async () => {
    const resolved = await resolveCompute(PRICES, {
      computeType: 'gpu', gpuType: 'mi325x', gpuCount: '2',
    }, ctx());

    expect(resolved).toEqual({ computeSku: 'gpu.mi325x.2x' });
  });

  it('lists candidates when the gpu type is ambiguous', async () => {
    const ambiguous = [
      gpuRate('gpu.a.1x', 1, 'A100'),
      gpuRate('gpu.b.1x', 1, 'A100'),
    ];

    await expect(resolveCompute(ambiguous, {
      computeType: 'gpu', gpuType: 'a100',
    }, ctx())).rejects.toThrow(/Multiple GPU offers match \(1x A100, 1x A100\)/);
  });

  it('rejects unsupported gpu counts', async () => {
    await expect(resolveCompute(PRICES, {
      computeType: 'gpu', gpuCount: '3',
    }, ctx())).rejects.toThrow('--gpu-count must be 1, 2, 4, or 8');
  });

  it('picks the smallest cpu offer by default', async () => {
    const resolved = await resolveCompute(PRICES, { computeType: 'cpu' }, ctx());
    expect(resolved).toEqual({ computeSku: 'cpu.small' });
  });

  it('picks the requested core count', async () => {
    const resolved = await resolveCompute(PRICES, { computeType: 'cpu', cpuCores: '16' }, ctx());
    expect(resolved).toEqual({ computeSku: 'cpu.large' });
  });

  it('defaults to one GPU in plain mode', async () => {
    const plain = [gpuRate('gpu.mi325x.1x', 1, 'MI325X')];
    const resolved = await resolveCompute(plain, { computeType: 'gpu' }, ctx());
    expect(resolved).toEqual({ computeSku: 'gpu.mi325x.1x' });
  });

  it('defaults a bare count to the in-service model among priced offers', async () => {
    const catalog = [
      gpuRate('gpu.mi325x.1x', 1, 'MI325X'),
      gpuRate('gpu.b300.1x', 1, 'B300'),
      gpuRate('gpu.h100.1x', 1, 'H100'),
    ];

    const resolved = await resolveCompute(catalog, { computeType: 'gpu' }, ctx());
    expect(resolved).toEqual({ computeSku: 'gpu.mi325x.1x' });
  });
});

describe('resolveImage', () => {
  it('replaces a template\'s image or registry key', () => {
    expect(resolveImage(CREDENTIALS, { image: 'nginx:1' }, true)).toEqual({ containerImage: 'nginx:1' });
    expect(resolveImage(CREDENTIALS, { registry: 'Private Hub' }, true)).toEqual({ registryCredentialId: CREDENTIALS[0]?.id });
  });

  it('rejects registry without an image or a template', () => {
    expect(() => resolveImage(CREDENTIALS, { registry: 'Private Hub' }, false))
      .toThrow('--registry applies only with --image or a template.');
  });

  it('resolves a private image with a named credential', () => {
    expect(resolveImage(CREDENTIALS, { image: 'registry.example.com/x:1', registry: 'private hub' }, false))
      .toEqual({ containerImage: 'registry.example.com/x:1', registryCredentialId: CREDENTIALS[0]?.id });
  });

  it('rejects too-short image references', () => {
    expect(() => resolveImage([], { image: 'x:' }, false)).toThrow('Enter a container image.');
  });

  it('requires an explicit choice in plain mode', () => {
    expect(() => resolveImage([], {}, false)).toThrow('Pass --image, --template or --template-id.');
  });
});

describe('template versions', () => {
  const versioned = template({
    id: '11111111-1111-4111-8111-111111111119',
    slug: 'rocm-pytorch',
    name: 'PyTorch (ROCm)',
    visibility: 'public',
    versions: [{ version: '2.12.0', image: 'r:2.12' }, { version: '2.11.0', image: 'r:2.11' }],
  });

  it('reads a version after the colon in a template reference', () => {
    expect(splitTemplateVersion('featherless/rocm-pytorch:2.11.0')).toEqual({ ref: 'featherless/rocm-pytorch', version: '2.11.0' });
    expect(splitTemplateVersion('trainer')).toEqual({ ref: 'trainer' });
  });

  it('accepts only versions the official template lists', () => {
    expect(checkTemplateVersion(versioned, '2.11.0')).toBe('2.11.0');
    expect(checkTemplateVersion(versioned, undefined)).toBeUndefined();
    expect(() => checkTemplateVersion(versioned, '9.9.9')).toThrow('PyTorch (ROCm) has no version 9.9.9. Versions: 2.12.0, 2.11.0.');
    expect(() => checkTemplateVersion(MINE, '1')).toThrow('Only official templates have versions');
  });
});

describe('resolveTemplateRef', () => {
  it('finds your template first, then your organization\'s, then an official one', () => {
    expect(resolveTemplateRef(TEMPLATES, 'trainer')).toBe(MINE);
    expect(resolveTemplateRef(TEMPLATES, 'serve')).toBe(SHARED_SERVE);
    expect(resolveTemplateRef(TEMPLATES, 'rocm-pytorch')).toBe(PYTORCH);
    expect(resolveTemplateRef([...TEMPLATES, MY_PYTORCH], 'rocm-pytorch')).toBe(MY_PYTORCH);
  });

  it('reaches a shadowed template through its namespace', () => {
    expect(resolveTemplateRef(TEMPLATES, 'org/trainer')).toBe(SHARED);
    expect(resolveTemplateRef([...TEMPLATES, MY_PYTORCH], 'featherless/rocm-pytorch')).toBe(PYTORCH);
    expect(() => resolveTemplateRef(TEMPLATES, 'org/rocm-pytorch')).toThrow('No template matching "org/rocm-pytorch"');
  });

  it('finds a template by id or short id', () => {
    expect(resolveTemplateRef(TEMPLATES, SHARED.id.toUpperCase())).toBe(SHARED);
    expect(resolveTemplateRef(TEMPLATES, SHARED_SERVE.shortId)).toBe(SHARED_SERVE);
  });

  it('still matches part of an official template\'s display name', () => {
    expect(resolveTemplateRef(TEMPLATES, 'PyTorch')).toBe(PYTORCH);

    expect(() => resolveTemplateRef([...TEMPLATES, template({ id: 't9', slug: 'rocm-pytorch-nightly', name: 'PyTorch nightly', visibility: 'public' })], 'pytorch'))
      .toThrow('Multiple templates match "pytorch"');
  });
});

describe('templateReference', () => {
  it('names a template bare unless a closer one has its name', () => {
    expect(TEMPLATES.map(item => templateReference(item, TEMPLATES))).toEqual(['rocm-pytorch', 'trainer', 'org/trainer', 'serve']);
    expect(templateReference(PYTORCH, [...TEMPLATES, MY_PYTORCH])).toBe('featherless/rocm-pytorch');
  });
});

describe('resolveVolume', () => {
  it('matches exact names and unique substrings', () => {
    const volumes = [volume(), volume({ id: '44444444-4444-4444-8444-444444444444', name: 'datasets' })];
    expect(resolveVolume(volumes, 'data')).toBe(volumes[0]?.id);
    expect(resolveVolume(volumes, 'sets')).toBe(volumes[1]?.id);
    expect(resolveVolume(volumes, volumes[0]!.id)).toBe(volumes[0]?.id);
  });

  it('errors on unknown references', () => {
    expect(() => resolveVolume([volume()], 'missing')).toThrow(
      'No network volume matching "missing". Run featherless-cloud volume ls.',
    );
  });

  it('refuses volumes that are not attachable', () => {
    expect(() => resolveVolume([volume({ state: 'provisioning' })], 'data'))
      .toThrow('Volume "data" is provisioning. Wait for it to become available.');
  });
});

describe('findInstance', () => {
  it('matches exact names, unique substrings, and uuids', () => {
    const found = instance();
    const list = [found, instance({ id: '66666666-6666-4666-8666-666666666666', shortId: 'dtspr0d012', name: 'datasets' })];
    expect(findInstance(list, 'tmux-bundle-ubuntu')).toBe(found);
    expect(findInstance(list, 'bundle')).toBe(found);
    expect(findInstance(list, found.id)).toBe(found);
  });

  it('lists every candidate on an ambiguous substring', () => {
    const list = [
      instance({ name: 'train-a' }),
      instance({ id: '66666666-6666-4666-8666-666666666666', shortId: 'trnnb00012', name: 'train-b' }),
    ];

    expect(() => findInstance(list, 'train')).toThrow(
      'Multiple instances match "train": train-a, train-b. Use the exact name or short id.',
    );
  });

  it('errors on unknown references', () => {
    expect(() => findInstance([instance()], 'missing')).toThrow(
      'No instance matching "missing". Run featherless-cloud instance list.',
    );

    expect(() => findInstance([instance()], '99999999-9999-4999-8999-999999999999')).toThrow(
      'No instance matching "99999999-9999-4999-8999-999999999999". Run featherless-cloud instance list.',
    );
  });
});

describe('resolveInstanceId', () => {
  it('passes uuid and short id references through without listing', async () => {
    const client = {
      listInstances: async () => {
        throw new Error('no call');
      },
    } as unknown as CloudClient;

    const uuid = instance().id;
    await expect(resolveInstanceId(client, uuid)).resolves.toBe(uuid);
    await expect(resolveInstanceId(client, 'tmxbnd1234')).resolves.toBe('tmxbnd1234');
  });

  it('resolves a name from a later page', async () => {
    const client = clientWithPages(instance(), instance({ id: '66666666-6666-4666-8666-666666666666', shortId: 'dtspr0d012', name: 'datasets' }));
    await expect(resolveInstanceId(client, 'datasets')).resolves.toBe('66666666-6666-4666-8666-666666666666');
  });

  it('errors when no page matches', async () => {
    const client = clientWithPages(instance());
    await expect(resolveInstanceId(client, 'missing')).rejects.toThrow(CliError);
  });
});

describe('instance names', () => {
  it('generates console-parity random names', () => {
    expect(randomInstanceName()).toMatch(
      /^(amber|bold|bright|calm|clever|cosmic|eager|lucky|rapid|steady)-(comet|falcon|forge|nebula|orbit|phoenix|rocket|summit|tiger|vector)-\d{3}$/,
    );
  });

  it('rejects uppercase and underscores', () => {
    expect(() => assertInstanceName('bad_name')).toThrow(/lowercase letters, digits, and hyphens/);
    expect(() => assertInstanceName('-lead')).toThrow(/lowercase/);
    expect(() => assertInstanceName('good-name-1')).not.toThrow();
  });
});

describe('requireDeletion', () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('refuses in non-interactive mode without --yes', async () => {
    const exit = vi.spyOn(process, 'exit').mockImplementation(((code?: number) => {
      throw new Error(`exit:${code}`);
    }) as never);

    const fail = vi.spyOn(console, 'error').mockImplementation(() => undefined);

    await expect(requireDeletion(ctx(), 'instance abc', false)).rejects.toThrow('exit:1');
    expect(fail).toHaveBeenCalledWith(expect.stringContaining('Refusing to delete instance abc without --yes.'));
    expect(exit).toHaveBeenCalledWith(1);
  });

  it('proceeds with --yes without prompting', async () => {
    await expect(requireDeletion(ctx(), 'volume v', true)).resolves.toBeUndefined();
  });
});

describe('newContent', () => {
  it('emits only appended lines', () => {
    expect(newContent('a\nb\n', 'a\nb\nc\n')).toBe('c\n');
  });

  it('skips the overlap when the tail window slides', () => {
    expect(newContent('a\nb\nc\nd', 'c\nd\ne')).toBe('e');
  });

  it('prints everything after a container restart', () => {
    expect(newContent('old output', 'fresh start\nready')).toBe('fresh start\nready');
  });

  it('emits nothing when the tail is unchanged', () => {
    expect(newContent('same', 'same')).toBe('');
  });
});

describe('resolveSshKeyRef', () => {
  const keys: SshKey[] = [
    { id: '33333333-3333-4333-8333-333333333333', name: 'laptop', fingerprint: 'SHA256:a', createdAt: '2026-10-26T00:00:00.000Z' },
    { id: '44444444-4444-4444-8444-444444444444', name: 'ci', fingerprint: 'SHA256:b', createdAt: '2026-10-26T00:00:00.000Z' },
    { id: '55555555-5555-4555-8555-555555555555', name: 'ci', fingerprint: 'SHA256:c', createdAt: '2026-10-26T00:00:00.000Z' },
  ];

  it('matches a name case-insensitively, or an id', () => {
    expect(resolveSshKeyRef(keys, 'Laptop').id).toBe(keys[0]?.id);
    expect(resolveSshKeyRef(keys, '44444444-4444-4444-8444-444444444444').fingerprint).toBe('SHA256:b');
  });

  it('asks for the id when a name is shared', () => {
    expect(() => resolveSshKeyRef(keys, 'ci')).toThrow('Several SSH keys are named "ci"');
  });

  it('rejects an unknown key', () => {
    expect(() => resolveSshKeyRef(keys, 'nope')).toThrow('No SSH key matching "nope"');
  });
});

describe('parsePublicKey', () => {
  it('keeps the key line and its comment', () => {
    expect(parsePublicKey('ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI me@laptop\n'))
      .toEqual({ publicKey: 'ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI me@laptop', comment: 'me@laptop' });
  });

  it('accepts a key without a comment', () => {
    expect(parsePublicKey('ecdsa-sha2-nistp256 AAAAE2VjZHNh').comment).toBeUndefined();
  });

  it('refuses a private key', () => {
    expect(() => parsePublicKey('-----BEGIN OPENSSH PRIVATE KEY-----\nabc\n-----END OPENSSH PRIVATE KEY-----'))
      .toThrow('That is a private key.');
  });

  it('refuses anything else', () => {
    expect(() => parsePublicKey('hello world')).toThrow('That is not an OpenSSH public key.');
  });
});
