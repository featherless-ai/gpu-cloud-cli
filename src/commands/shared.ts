import * as p from '@clack/prompts';
import { CliError } from '../error';
import type { CliContext } from '../context';
import type { Command } from 'commander';
import { computeTypeOf, type PriceRate } from '../types/pricing';
import type { Template } from '../types/templates';
import type { NetworkVolumeDto } from '../types/volume';
import type { InstanceDto } from '../types/instance';
import type { CloudClient } from '../clients/cloud';
import type { RegistryCredential, SshKey } from '../types/instance';
import { loadConfig } from '../config';
import type { Completer, CompleterContext } from '../completions';

/** Case-insensitive prefix filter for completer candidates: `values.filter(startsWith(word))`. */
export const startsWith = (word: string) => (value: string) => value.toLowerCase().startsWith(word.toLowerCase());

export interface NameValue {
  name: string;
  value: string;
}

/** Splits repeated `NAME=VALUE` flag values on the first `=`. */
export function parseRepeatable(values: string[] | undefined, label: string): NameValue[] {
  return (values ?? []).map((entry) => {
    const index = entry.indexOf('=');

    if (index < 1) {
      throw new CliError(`--${label} expects NAME=VALUE, got "${entry}"`);
    }

    return { name: entry.slice(0, index), value: entry.slice(index + 1) };
  });
}

/** Repeated `--port` values as public ports; `none` alone means no ports. */
export function parsePorts(values: string[] | undefined): number[] {
  if (values?.length === 1 && values[0] === 'none') {
    return [];
  }

  const ports = (values ?? []).map((value) => {
    const port = Number(value);

    if (!/^\d+$/.test(value) || port < 1 || port > 65_535) {
      throw new CliError(`--port expects a port from 1 to 65535, got "${value}"`);
    }

    return port;
  });

  if (new Set(ports).size !== ports.length) {
    throw new CliError('List each port once.');
  }

  return ports;
}

/** Validates environment-variable names (shell-compatible, unique). */
export function toEnvironment(pairs: NameValue[]): NameValue[] {
  const names = pairs.map(pair => pair.name);
  const invalid = names.some(name => !/^[A-Za-z_][A-Za-z0-9_]*$/.test(name));

  if (invalid || new Set(names).size !== names.length) {
    throw new CliError('Variable names must be valid and unique.');
  }

  return pairs;
}

/** Mount-path regex from the console's instance launcher. */
const MOUNT_PATH_PATTERN = /^\/(?!\/)(?:[^/]+\/)*[^/]+$/;

export interface MountRef {
  networkVolumeId: string;
  mountPath: string;
}

export function validateMounts(mounts: MountRef[]): void {
  if (mounts.some(mount => !mount.networkVolumeId)) {
    throw new CliError('Choose a network volume for each mount.');
  }

  if (new Set(mounts.map(mount => mount.networkVolumeId)).size !== mounts.length) {
    throw new CliError('Each network volume can be mounted only once.');
  }

  const paths = mounts.map(mount => mount.mountPath);

  if (paths.some(path => !MOUNT_PATH_PATTERN.test(path))) {
    throw new CliError('Use an absolute mount path such as /workspace.');
  }

  const overlaps = paths.some((path, i) => paths.some((other, j) =>
    i !== j && (other === path || other.startsWith(`${path}/`) || path.startsWith(`${other}/`))));

  if (overlaps) {
    throw new CliError('Mount paths must not overlap.');
  }
}

/** `<name>:` for `--volume <name:path>`; the mount path after the colon is free text. */
export const volumeMounts: Completer = async (word, ctx) =>
  word.includes(':') ? [] : (await volumes(word, ctx)).map(name => `${name}:`);

export const UUID_PATTERN = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;
const SHORT_ID_PATTERN = /^[0123456789bcdfghjkmnpqrstvwxyz]{10}$/;

export interface ComputeOptions {
  computeType: 'gpu' | 'cpu';
  gpuType?: string;
  gpuCount?: string;
  cpuCores?: string;
  /** Interactive runs prompt instead of failing when no flags were given. */
  interactive?: boolean;
}

/** Resolves friendly --gpu-type/--gpu-count/--cpu-cores flags to a compute SKU. */
export async function resolveCompute(prices: PriceRate[], opts: ComputeOptions, ctx: CliContext): Promise<{ computeSku: string }> {
  const explicit = opts.computeType !== 'gpu' || opts.gpuType !== undefined
    || opts.gpuCount !== undefined || opts.cpuCores !== undefined;

  if (opts.computeType === 'cpu') {
    return { computeSku: resolveCpuSku(prices, opts.cpuCores) };
  }

  if (!explicit) {
    if (opts.interactive) {
      return selectCompute(prices, ctx);
    }

    return { computeSku: resolveGpuSku(prices, undefined, '1') };
  }

  return { computeSku: resolveGpuSku(prices, opts.gpuType, opts.gpuCount) };
}

function resolveGpuSku(prices: PriceRate[], gpuType: string | undefined, gpuCountFlag: string | undefined): string {
  const gpuCount = gpuCountFlag === undefined ? 1 : Number(gpuCountFlag);

  if (![1, 2, 4, 8].includes(gpuCount)) {
    throw new CliError('--gpu-count must be 1, 2, 4, or 8');
  }

  let candidates = prices.filter(rate =>
    computeTypeOf(rate) === 'gpu' && rate.unit === 'gpu_hour' && rate.gpuCount === gpuCount);

  if (gpuType) {
    const needle = gpuType.toLowerCase();

    candidates = candidates.filter(rate =>
      (rate.resources?.gpuModel ?? rate.displayName).toLowerCase().includes(needle));
  }

  if (candidates.length > 1 && !gpuType) {
    // Priced models the fleet may not stock yet make bare --gpu-count ambiguous;
    // default to the in-service model, as the console does.
    const stocked = candidates.filter(rate => /mi[-_ ]?325/i.test(rate.resources?.gpuModel ?? ''));

    if (stocked.length === 1) {
      candidates = stocked;
    }
  }

  if (candidates.length > 1) {
    const options = candidates.map(rate => rate.displayName).join(', ');
    throw new CliError(`Multiple GPU offers match (${options}). Narrow with --gpu-type.`);
  }

  const pick = candidates[0];

  if (!pick) {
    throw new CliError('No GPU offer available. Run featherless-cloud pricing list.');
  }

  return pick.sku;
}

function cpuOffers(prices: PriceRate[]): PriceRate[] {
  return prices.filter(rate =>
    computeTypeOf(rate) === 'cpu' && rate.unit === 'vcpu_hour' && rate.resources !== undefined);
}

function resolveCpuSku(prices: PriceRate[], cpuCoresFlag: string | undefined): string {
  const candidates = cpuOffers(prices);

  if (candidates.length === 0) {
    throw new CliError('No CPU offer available. Run featherless-cloud pricing list.');
  }

  const coresOf = (rate: PriceRate) => rate.resources?.cpuCores ?? Number.POSITIVE_INFINITY;

  if (cpuCoresFlag === undefined) {
    const smallest = [...candidates].sort((a, b) => coresOf(a) - coresOf(b))[0];

    return smallest?.sku ?? candidates[0]?.sku ?? 'cpu';
  }

  const cores = Number(cpuCoresFlag);
  const match = candidates.find(rate => coresOf(rate) === cores);

  if (!match) {
    const sizes = candidates.map(rate => String(coresOf(rate))).join(', ');
    throw new CliError(`No CPU offer with ${cores} cores. Available core counts: ${sizes}.`);
  }

  return match.sku;
}

type Workload = 'instance' | 'run-job';

async function computeRates(ctx: CompleterContext, workload: Workload, kind: 'gpu' | 'cpu'): Promise<PriceRate[]> {
  const unit = kind === 'gpu' ? 'gpu_hour' : 'vcpu_hour';

  return (await (await ctx.cloud()).pricing(workload)).filter(rate => computeTypeOf(rate) === kind && rate.unit === unit);
}

const numbers = (values: number[]) => [...new Set(values)].sort((a, b) => a - b).map(String);

/** GPU models on offer, lowercased like the --gpu-type examples. */
export function gpuTypes(workload: Workload): Completer {
  return async (word, ctx) => {
    const models = (await computeRates(ctx, workload, 'gpu')).flatMap(rate => rate.resources?.gpuModel?.toLowerCase() ?? []);

    return [...new Set(models)].filter(startsWith(word));
  };
}

export function gpuCounts(workload: Workload): Completer {
  return async (word, ctx) =>
    numbers((await computeRates(ctx, workload, 'gpu')).flatMap(rate => rate.gpuCount ?? [])).filter(startsWith(word));
}

export function cpuCores(workload: Workload): Completer {
  return async (word, ctx) =>
    numbers((await computeRates(ctx, workload, 'cpu')).flatMap(rate => rate.resources?.cpuCores ?? [])).filter(startsWith(word));
}

/** Picks the compute offer interactively when no compute flags were given. */
export async function selectCompute(prices: PriceRate[], ctx: CliContext): Promise<{ computeSku: string }> {
  const kind = await p.select({
    message: 'What compute do you need?',
    options: [
      { value: 'gpu', label: 'GPU' },
      { value: 'cpu', label: 'CPU' },
    ],
    initialValue: 'gpu',
  });

  if (p.isCancel(kind)) {
    ctx.log('Cancelled.');
    process.exit(0);
  }

  if (kind === 'cpu') {
    const cpuPrices = prices.filter(rate =>
      computeTypeOf(rate) === 'cpu' && rate.unit === 'vcpu_hour' && rate.resources !== undefined);

    if (cpuPrices.length === 0) {
      throw new CliError('No CPU offer available. Run featherless-cloud pricing list.');
    }

    const coreChoices = [...new Set(cpuPrices.map(rate => rate.resources?.cpuCores))]
      .filter((n): n is number => n !== undefined)
      .sort((a, b) => a - b)
      .map(n => ({ value: String(n), label: `${n} vCPU` }));

    const chosen = await p.select({
      message: 'How many vCPU cores?',
      options: coreChoices,
    });

    if (p.isCancel(chosen)) {
      ctx.log('Cancelled.');
      process.exit(0);
    }

    return { computeSku: resolveCpuSku(prices, String(chosen)) };
  }

  const count = await p.select({
    message: 'How many GPUs?',
    options: [1, 2, 4, 8].map(n => ({ value: String(n), label: `${n} GPU${n === 1 ? '' : 's'}` })),
    initialValue: '1',
  });

  if (p.isCancel(count)) {
    ctx.log('Cancelled.');
    process.exit(0);
  }

  const gpuPrices = prices.filter(rate =>
    computeTypeOf(rate) === 'gpu' && rate.unit === 'gpu_hour' && rate.gpuCount === Number(count));

  if (gpuPrices.length === 0) {
    throw new CliError(`No GPU offer with ${count} GPU(s). Run featherless-cloud pricing list.`);
  }

  const soleGpu = gpuPrices[0];

  if (gpuPrices.length === 1 && soleGpu) {
    return { computeSku: soleGpu.sku };
  }

  const sku = await p.select({
    message: 'Which GPU offer?',
    options: gpuPrices.map(rate => ({ value: rate.sku, label: rate.displayName })),
  });

  if (p.isCancel(sku)) {
    ctx.log('Cancelled.');
    process.exit(0);
  }

  return { computeSku: sku as string };
}

export interface ImageOptions {
  image?: string;
  registry?: string;
}

export interface ImageFields {
  containerImage?: string;
  registryCredentialId?: string;
}

/** Resolves --image/--registry; with a template they replace its image or registry key. */
export function resolveImage(credentials: RegistryCredential[], opts: ImageOptions, hasTemplate: boolean): ImageFields {
  if (opts.registry && !opts.image && !hasTemplate) {
    throw new CliError('--registry applies only with --image or a template.');
  }

  const fields: ImageFields = {};

  if (opts.image) {
    const reference = opts.image.trim();

    if (reference.length < 3) {
      throw new CliError('Enter a container image.');
    }

    fields.containerImage = reference;
  }

  if (opts.registry) {
    fields.registryCredentialId = resolveCredentialRef(credentials, opts.registry);
  }

  if (!hasTemplate && !fields.containerImage) {
    throw new CliError('Pass --image, --template or --template-id.');
  }

  return fields;
}

/** Picks a template interactively when neither a template nor --image was given. */
export async function selectImage(
  templates: Template[],
  ctx: CliContext,
): Promise<{ template: Template } | { containerImage: string }> {
  if (templates.length === 0) {
    const image = await p.text({
      message: 'No templates are available. Enter a container image:',
      validate: value => ((value ?? '').trim().length >= 3 ? undefined : 'Enter a container image.'),
    });

    if (p.isCancel(image)) {
      ctx.log('Cancelled.');
      process.exit(0);
    }

    return { containerImage: String(image).trim() };
  }

  const id = await p.select({
    message: 'Which template?',
    options: templates.map(template => ({
      value: template.id,
      label: template.official ? `${template.name} ${template.version.version}` : templateReference(template, templates),
    })),
  });

  if (p.isCancel(id)) {
    ctx.log('Cancelled.');
    process.exit(0);
  }

  return { template: templates.find(template => template.id === id)! };
}

const VISIBILITY_ORDER = ['private', 'organization', 'public'] as const;

/**
 * What to type for a template: its name, qualified only when one closer to you has the
 * same name. A bare name finds your template, then your organization's, then an official one.
 */
export function templateReference(template: Template, templates: Template[]): string {
  const closer = templates.some(other => other.slug === template.slug
    && VISIBILITY_ORDER.indexOf(other.visibility) < VISIBILITY_ORDER.indexOf(template.visibility));

  if (!closer) {
    return template.slug;
  }

  return template.official ? `featherless/${template.slug}` : `org/${template.slug}`;
}

/** Resolves --template: an id, a name, org/<name>, featherless/<name>, or an official template's display name. */
export function resolveTemplateRef(templates: Template[], ref: string): Template {
  const needle = ref.toLowerCase();
  const byId = templates.find(template => template.id === needle || template.shortId === needle || template.version.id === needle);

  if (byId) {
    return byId;
  }

  const separator = needle.indexOf('/');
  const scope = separator > 0 ? needle.slice(0, separator) : undefined;
  const slug = separator > 0 ? needle.slice(separator + 1) : needle;
  let match: Template | undefined;

  if (scope === 'org') {
    match = templates.find(template => template.visibility === 'organization' && template.slug === slug);
  } else if (scope === 'featherless') {
    match = templates.find(template => template.official && template.slug === slug);
  } else {
    match = VISIBILITY_ORDER
      .map(visibility => templates.find(template => template.visibility === visibility && template.slug === slug))
      .find(Boolean);
  }

  if (match) {
    return match;
  }

  // An official template's display name, as `--template pytorch` has always worked.
  const official = templates.filter(template => template.official);
  const exact = official.filter(template => template.name.toLowerCase() === needle);
  const candidates = exact.length ? exact : official.filter(template => template.name.toLowerCase().includes(needle));

  if (candidates.length === 1) {
    return candidates[0]!;
  }

  if (candidates.length > 1) {
    const names = candidates.map(template => templateReference(template, templates)).join(', ');
    throw new CliError(`Multiple templates match "${ref}" (${names}). Be more specific.`);
  }

  throw new CliError(`No template matching "${ref}". List them with featherless-cloud templates list.`);
}

/** The template --template or --template-id names, if either was given. */
/** `featherless/rocm-pytorch:2.11.0` names an official template's version; template names never hold a colon. */
export function splitTemplateVersion(ref: string): { ref: string; version?: string } {
  const separator = ref.indexOf(':');
  return separator > 0 ? { ref: ref.slice(0, separator), version: ref.slice(separator + 1) } : { ref };
}

/** A version must be one the official template lists. */
export function checkTemplateVersion(template: Template, version: string | undefined): string | undefined {
  if (!version) {
    return undefined;
  }

  if (!template.official) {
    throw new CliError(`Only official templates have versions, and ${template.name} is not one.`);
  }

  if (!template.versions.some(entry => entry.version === version)) {
    throw new CliError(`${template.name} has no version ${version}. Versions: ${template.versions.map(entry => entry.version).join(', ')}.`);
  }

  return version;
}

export interface TemplateChoice {
  template: Template;
  /** An official version other than the current one. */
  version?: string;
}

export async function findTemplate(
  client: CloudClient,
  opts: { template?: string; templateId?: string; templateVersion?: string },
): Promise<TemplateChoice | null> {
  if (opts.template && opts.templateId) {
    throw new CliError('Choose either --template or --template-id.');
  }

  let template: Template | null = null;
  let version = opts.templateVersion;

  if (opts.templateId) {
    template = await client.template(opts.templateId);
  }

  if (opts.template) {
    const split = splitTemplateVersion(opts.template);

    if (split.version && opts.templateVersion) {
      throw new CliError('Give the version once: --template name:version or --template-version.');
    }

    version = split.version ?? version;
    template = resolveTemplateRef(await client.templates(), split.ref);
  }

  if (!template) {
    if (opts.templateVersion) {
      throw new CliError('--template-version needs --template or --template-id.');
    }

    return null;
  }

  return { template, version: checkTemplateVersion(template, version) };
}

export const templates: Completer = async (word, ctx) => {
  const usable = (await (await ctx.cloud()).templates()).filter(template => template.enabled);
  return usable.map(template => templateReference(template, usable)).filter(startsWith(word));
};

export const templateIds: Completer = async (word, ctx) =>
  (await (await ctx.cloud()).templates()).map(template => template.shortId).filter(startsWith(word));

export function parseVolumeFlags(values: string[] | undefined): Array<{ volume: string; mountPath: string }> {
  return (values ?? []).map((entry) => {
    const separator = entry.lastIndexOf(':');

    if (separator < 1 || separator === entry.length - 1) {
      throw new CliError(`--volume expects NAME:PATH, got "${entry}"`);
    }

    return { volume: entry.slice(0, separator), mountPath: entry.slice(separator + 1) };
  });
}

export function parseDisk(value: string): number {
  const disk = Number(value);

  if (!Number.isInteger(disk) || disk < 20 || disk > 2000) {
    throw new CliError('Container disk must be between 20 and 2000 GB.');
  }

  return disk;
}

/** Whether any compute flag was typed, so a template's compute should give way. */
export function computeFlagsGiven(cmd: Command, opts: { gpuType?: string; gpuCount?: string; cpuCores?: string }): boolean {
  return cmd.getOptionValueSource('computeType') === 'cli'
    || opts.gpuType !== undefined || opts.gpuCount !== undefined || opts.cpuCores !== undefined;
}

export function resolveCredentialRef(credentials: RegistryCredential[], ref: string): string {
  if (UUID_PATTERN.test(ref)) {
    const byId = credentials.find(credential => credential.id === ref.toLowerCase());

    if (byId) {
      return byId.id;
    }
  }

  const needle = ref.toLowerCase();
  const match = credentials.find(credential => credential.name.toLowerCase() === needle);

  if (match) {
    return match.id;
  }

  throw new CliError(`No registry key matching "${ref}". List them with featherless-cloud registry-keys list.`);
}

export const registries: Completer = async (word, ctx) =>
  (await (await ctx.cloud()).registryCredentials()).map(credential => credential.name).filter(startsWith(word));

export function resolveSshKeyRef(keys: SshKey[], ref: string): SshKey {
  const byId = keys.find(key => key.id === ref.toLowerCase());

  if (byId) {
    return byId;
  }

  const needle = ref.toLowerCase();
  const matches = keys.filter(key => key.name.toLowerCase() === needle);

  if (matches.length === 1) {
    return matches[0]!;
  }

  if (matches.length > 1) {
    throw new CliError(`Several SSH keys are named "${ref}". Use the ID from featherless-cloud ssh-keys list.`);
  }

  throw new CliError(`No SSH key matching "${ref}". List them with featherless-cloud ssh-keys list.`);
}

export const sshKeys: Completer = async (word, ctx) =>
  (await (await ctx.cloud()).sshKeys()).map(key => key.name).filter(startsWith(word));

/** Finds a volume by uuid, exact name, or unique substring, in any state. */
export function findVolume(volumes: NetworkVolumeDto[], ref: string): NetworkVolumeDto {
  let volume: NetworkVolumeDto | undefined;

  if (UUID_PATTERN.test(ref)) {
    volume = volumes.find(candidate => candidate.id === ref.toLowerCase());
  }

  if (!volume) {
    const exact = volumes.filter(candidate => candidate.name === ref);

    if (exact.length === 1) {
      volume = exact[0];
    }
  }

  if (!volume) {
    const needle = ref.toLowerCase();
    const partial = volumes.filter(candidate => candidate.name.toLowerCase().includes(needle));

    if (partial.length === 1) {
      volume = partial[0];
    }
  }

  if (!volume) {
    throw new CliError(`No network volume matching "${ref}". Run featherless-cloud volume ls.`);
  }

  return volume;
}

/** Resolves a volume reference to an attachable (available/in_use) volume id. */
export function resolveVolume(volumes: NetworkVolumeDto[], ref: string): string {
  const volume = findVolume(volumes, ref);

  if (volume.state !== 'available' && volume.state !== 'in_use') {
    throw new CliError(`Volume "${volume.name}" is ${volume.state}. Wait for it to become available.`);
  }

  return volume.id;
}

export const volumes: Completer = async (word, ctx) =>
  (await (await ctx.cloud()).listVolumes()).map(volume => volume.name).filter(startsWith(word));

/** Finds an instance by uuid, exact name, or unique substring, in any state. */
export function findInstance(instances: InstanceDto[], ref: string): InstanceDto {
  if (UUID_PATTERN.test(ref)) {
    const byId = instances.find(candidate => candidate.id === ref.toLowerCase());

    if (byId) {
      return byId;
    }
  }

  const exact = instances.filter(candidate => candidate.name === ref);

  if (exact.length > 1) {
    throw new CliError(`Multiple instances match "${ref}": ${exact.map(match => match.name).join(', ')}. Use the exact name or short id.`);
  }

  if (exact.length === 1) {
    return exact[0]!;
  }

  const needle = ref.toLowerCase();
  const partial = instances.filter(candidate => candidate.name.toLowerCase().includes(needle));

  if (partial.length > 1) {
    throw new CliError(`Multiple instances match "${ref}": ${partial.map(match => match.name).join(', ')}. Use the exact name or short id.`);
  }

  if (partial.length === 1) {
    return partial[0]!;
  }

  throw new CliError(`No instance matching "${ref}". Run featherless-cloud instance list.`);
}

/** Resolves an instance reference (uuid, short id, name, unique substring) to the uuid the API expects. */
export async function resolveInstanceId(client: CloudClient, ref: string): Promise<string> {
  if (UUID_PATTERN.test(ref) || SHORT_ID_PATTERN.test(ref)) {
    return ref;
  }

  const all: InstanceDto[] = [];

  for (let page = 1; ; page += 1) {
    const { items, pagination } = await client.listInstances({ page, limit: 100 });
    all.push(...items);

    if (page >= pagination.totalPages) {
      break;
    }
  }

  return findInstance(all, ref).id;
}

/** Instance names and short ids. */
export const instances: Completer = async (word, ctx) => {
  const { items } = await (await ctx.cloud()).listInstances({ page: 1, limit: 100 });

  return [...new Set(items.flatMap(instance => [instance.name, instance.shortId]))].filter(startsWith(word));
};

/** Run Job short ids; job commands take a uuid or short id. */
export const jobs: Completer = async (word, ctx) => {
  const { items } = await (await ctx.cloud()).listJobs({ page: 1, limit: 100 });

  return items.map(job => job.shortId).filter(startsWith(word));
};

const NAME_ADJECTIVES = ['amber', 'bold', 'bright', 'calm', 'clever', 'cosmic', 'eager', 'lucky', 'rapid', 'steady'];
const NAME_NOUNS = ['comet', 'falcon', 'forge', 'nebula', 'orbit', 'phoenix', 'rocket', 'summit', 'tiger', 'vector'];

/** Random friendly instance name, matching the console launcher. */
export function randomInstanceName(): string {
  const values = crypto.getRandomValues(new Uint32Array(3));
  const adjective = NAME_ADJECTIVES[(values[0] ?? 0) % NAME_ADJECTIVES.length];
  const noun = NAME_NOUNS[(values[1] ?? 0) % NAME_NOUNS.length];
  const suffix = String((values[2] ?? 0) % 1000).padStart(3, '0');

  return `${adjective ?? NAME_ADJECTIVES[0]}-${noun ?? NAME_NOUNS[0]}-${suffix}`;
}

/** The naming rule the API enforces. */
export function assertInstanceName(name: string): void {
  if (!/^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/.test(name)) {
    throw new CliError('Instance names must be lowercase letters, digits, and hyphens (max 63, no leading/trailing hyphen).');
  }
}

/** Confirms destructive operations; non-interactive runs need --yes. */
export async function requireDeletion(ctx: CliContext, label: string, yesFlag: boolean): Promise<void> {
  if (ctx.mode === 'interactive' && !yesFlag) {
    const confirmed = await p.confirm({ message: `Delete ${label}? This cannot be undone.` });

    if (p.isCancel(confirmed) || !confirmed) {
      ctx.log('Cancelled.');
      process.exit(0);
    }

    return;
  }

  if (!yesFlag) {
    ctx.fatal(`Refusing to delete ${label} without --yes.`);
  }
}

export const profiles: Completer = async word => Object.keys((await loadConfig()).profiles).filter(startsWith(word));
