import type { Command } from 'commander';
import { CliCommand } from '../../completions';
import { createContext } from '../../context';
import { resolveAuth } from '../../config';
import { CloudClient, type CreateInstanceBody } from '../../clients/cloud';
import { CliError } from '../../error';
import {
  assertInstanceName,
  cpuCores,
  gpuCounts,
  gpuTypes,
  parsePorts,
  parseRepeatable,
  profiles,
  randomInstanceName,
  registries,
  resolveCompute,
  resolveImage,
  resolveVolume,
  selectImage,
  templates,
  toEnvironment,
  validateMounts,
  volumeMounts,
} from '../shared';

interface CreateOptions {
  name?: string;
  image?: string;
  template?: string;
  registry?: string;
  computeType: 'gpu' | 'cpu';
  gpuType?: string;
  gpuCount?: string;
  cpuCores?: string;
  disk?: string;
  volume?: string[];
  command?: string;
  env?: string[];
  secret?: string[];
  port?: string[];
  profile?: string;
}

function parseDisk(value: string | undefined): number {
  if (value === undefined) {
    return 20;
  }

  const disk = Number(value);

  if (!Number.isInteger(disk) || disk < 20 || disk > 2000) {
    throw new CliError('Container disk must be between 20 and 2000 GB.');
  }

  return disk;
}

function parseVolumeFlags(values: string[] | undefined): Array<{ volume: string; mountPath: string }> {
  return (values ?? []).map((entry) => {
    const separator = entry.lastIndexOf(':');

    if (separator < 1 || separator === entry.length - 1) {
      throw new CliError(`--volume expects NAME:PATH, got "${entry}"`);
    }

    return { volume: entry.slice(0, separator), mountPath: entry.slice(separator + 1) };
  });
}

export const instanceCreateCommand = new CliCommand('create')
  .description('Create an instance')
  .option('--name <name>', 'instance name (random when omitted)')
  .option('--image <reference>', 'container image to run')
  .option('--template <name|id>', 'published template to launch', { complete: templates })
  .option('--registry <name|id>', 'registry credential for a private --image', { complete: registries })
  .option('--compute-type <type>', 'gpu or cpu', { choices: ['gpu', 'cpu'], defaultValue: 'gpu' })
  .option('--gpu-type <text>', 'GPU model substring, e.g. mi325x', { complete: gpuTypes('instance') })
  .option('--gpu-count <n>', 'GPUs per instance: 1, 2, 4, or 8', { complete: gpuCounts('instance') })
  .option('--cpu-cores <n>', 'vCPU cores for --compute-type cpu', { complete: cpuCores('instance') })
  .option('--disk <gb>', 'container disk in GB (20-2000)', '20')
  .option('--volume <name:path>', 'mount a network volume, repeatable', { parse: (v: string, prev: string[]) => [...prev, v], defaultValue: [], complete: volumeMounts })
  .option('--command <string>', 'override the start command (whitespace-split, no shell quoting)')
  .option('--env <K=V>', 'environment variable, repeatable', (v: string, prev: string[]) => [...prev, v], [])
  .option('--secret <K=V>', 'secret variable, repeatable', (v: string, prev: string[]) => [...prev, v], [])
  .option('--port <port>', 'serve this port at a public HTTPS address, repeatable', (v: string, prev: string[]) => [...prev, v], [])
  .option('--profile <name>', 'profile to use', { complete: profiles })
  .addHelpText('after', '\nCreation is retry-safe: re-running the same command within the\nidempotency window returns the original instance.')
  .action(async (options: CreateOptions, cmd: Command) => {
    const ctx = createContext(cmd);
    const auth = await resolveAuth(options.profile);
    const client = new CloudClient(auth.apiBase, auth.apiKey);

    const name = options.name ?? randomInstanceName();
    assertInstanceName(name);

    const [pricing, templates, volumes, credentials] = await ctx.spin('Loading configurator', async () => {
      const registryNeeded = Boolean(options.image && options.registry);

      return Promise.all([
        client.pricing(),
        options.template ? client.templates() : Promise.resolve([]),
        options.volume?.length ? client.listVolumes() : Promise.resolve([]),
        registryNeeded ? client.registryCredentials() : Promise.resolve([]),
      ]);
    });

    let image;

    if (!options.template && !options.image && ctx.mode === 'interactive') {
      const catalog = await ctx.spin('Loading templates', () => client.templates());
      image = await selectImage(catalog, ctx);
    } else {
      image = resolveImage(templates, credentials, {
        template: options.template,
        image: options.image,
        registry: options.registry,
      });
    }

    const compute = await resolveCompute(pricing, {
      computeType: options.computeType,
      gpuType: options.gpuType,
      gpuCount: options.gpuCount,
      cpuCores: options.cpuCores,
      interactive: ctx.mode === 'interactive',
    }, ctx);

    const mounts = parseVolumeFlags(options.volume).map(entry => ({
      networkVolumeId: resolveVolume(volumes, entry.volume),
      mountPath: entry.mountPath,
    }));

    if (mounts.length > 8) {
      throw new CliError('At most 8 network volumes can be mounted.');
    }

    validateMounts(mounts);

    const quota = await client.organization().catch(() => null);

    if (quota && quota.gpuLimit > 0 && quota.gpuCount >= quota.gpuLimit) {
      ctx.warn('Organization GPU limit already reached; the server may reject this.');
    }

    const body: CreateInstanceBody = {
      name,
      ...image,
      computeSku: compute.computeSku,
      ephemeralDiskGiB: parseDisk(options.disk),
    };

    if (mounts.length) {
      body.networkVolumeMounts = mounts;
    }

    if (options.command?.trim()) {
      body.command = options.command.trim().split(/\s+/);
    }

    const environment = toEnvironment(parseRepeatable(options.env, 'env'));

    if (environment.length) {
      body.environment = environment;
    }

    const secrets = toEnvironment(parseRepeatable(options.secret, 'secret'));

    if (secrets.length) {
      body.secrets = secrets;
    }

    const ports = parsePorts(options.port);

    if (ports.length) {
      body.publicPorts = ports;
    }

    const instance = await ctx.spin('Creating instance', () =>
      client.createInstance(body, crypto.randomUUID()));

    ctx.success(`Instance ${instance.name} (${instance.shortId}) queued. Track it with featherless-cloud instance get ${instance.shortId}.`);
    ctx.output(instance, item => ({ text: `STATE ${item.state}` }));
  });
