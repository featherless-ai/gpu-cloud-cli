import type { Command } from 'commander';
import { CliCommand } from '../../completions';
import { createContext } from '../../context';
import { resolveAuth } from '../../config';
import { CloudClient, type CreateInstanceBody } from '../../clients/cloud';
import { CliError } from '../../error';
import {
  assertInstanceName,
  computeFlagsGiven,
  cpuCores,
  findTemplate,
  gpuCounts,
  gpuTypes,
  parseDisk,
  parsePorts,
  parseRepeatable,
  parseVolumeFlags,
  profiles,
  randomInstanceName,
  registries,
  resolveCompute,
  resolveImage,
  resolveVolume,
  selectImage,
  templateIds,
  templates,
  toEnvironment,
  validateMounts,
  volumeMounts,
} from '../shared';
import type { Template } from '../../types/templates';

interface CreateOptions {
  name?: string;
  image?: string;
  template?: string;
  templateId?: string;
  registry?: string;
  computeType: 'gpu' | 'cpu';
  gpuType?: string;
  gpuCount?: string;
  cpuCores?: string;
  disk: string;
  volume?: string[];
  command?: string;
  env?: string[];
  secret?: string[];
  port?: string[];
  profile?: string;
}

export const instanceCreateCommand = new CliCommand('create')
  .description('Create an instance')
  .option('--name <name>', 'instance name (random when omitted)')
  .option('--template <name>', 'template to launch: a name, org/<name>, featherless/<name> or an id; other flags override it', { complete: templates })
  .option('--template-id <id>', 'template to launch, by id', { complete: templateIds })
  .option('--image <reference>', 'container image to run, or to replace the template\'s')
  .option('--registry <name|id>', 'registry credential for a private image', { complete: registries })
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

    const [template, pricing, volumes, credentials] = await ctx.spin('Loading configurator', async () => Promise.all([
      findTemplate(client, options),
      client.pricing(),
      options.volume?.length ? client.listVolumes() : Promise.resolve([]),
      options.registry ? client.registryCredentials() : Promise.resolve([]),
    ]));

    let chosen: Template | null = template;
    let image;

    if (!chosen && !options.image && ctx.mode === 'interactive') {
      const selection = await selectImage(await ctx.spin('Loading templates', () => client.templates()), ctx);
      chosen = 'template' in selection ? selection.template : null;
      image = 'containerImage' in selection ? { containerImage: selection.containerImage } : {};
    } else {
      image = resolveImage(credentials, options, Boolean(chosen));
    }

    const body: CreateInstanceBody = {
      name,
      ...(chosen ? { templateId: chosen.id } : {}),
      ...image,
    };

    // A template's compute and disk stand unless a flag says otherwise.
    if (!chosen?.computeSku || computeFlagsGiven(cmd, options)) {
      const compute = await resolveCompute(pricing, {
        computeType: options.computeType,
        gpuType: options.gpuType,
        gpuCount: options.gpuCount,
        cpuCores: options.cpuCores,
        interactive: ctx.mode === 'interactive',
      }, ctx);

      body.computeSku = compute.computeSku;
    }

    if (!chosen || cmd.getOptionValueSource('disk') === 'cli') {
      body.ephemeralDiskGiB = parseDisk(options.disk);
    }

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
