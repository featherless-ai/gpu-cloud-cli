import type { Command } from 'commander';
import type { CliCommand } from '../../completions';
import type { CloudClient, TemplateBody } from '../../clients/cloud';
import type { CliContext } from '../../context';
import { CliError } from '../../error';
import { TEMPLATE_VISIBILITIES, type TemplateVisibility } from '../../types/templates';
import {
  computeFlagsGiven,
  cpuCores,
  gpuCounts,
  gpuTypes,
  parseDisk,
  parsePorts,
  parseRepeatable,
  parseVolumeFlags,
  registries,
  resolveCompute,
  resolveCredentialRef,
  resolveTemplateRef,
  resolveVolume,
  templates,
  toEnvironment,
  validateMounts,
  volumeMounts,
} from '../shared';

export interface TemplateFlags {
  description?: string;
  visibility?: string;
  template?: string;
  image?: string;
  registry?: string;
  computeType: 'gpu' | 'cpu';
  gpuType?: string;
  gpuCount?: string;
  cpuCores?: string;
  disk?: string;
  volume: string[];
  command?: string;
  env: string[];
  secret: string[];
  port: string[];
}

const repeat = (value: string, previous: string[]) => [...previous, value];

/** The settings `templates create` and `templates update` take; a repeated flag replaces that whole list. */
export function withTemplateFlags(command: CliCommand): CliCommand {
  return command
    .option('--description <text>', 'what the template is for')
    .option('--visibility <visibility>', 'private, or organization to let every member launch it with its secrets', { choices: [...TEMPLATE_VISIBILITIES] })
    .option('--template <name>', 'start from an official template\'s image, command and environment', { complete: templates })
    .option('--image <reference>', 'container image to run')
    .option('--registry <name|id>', 'registry credential for a private --image', { complete: registries })
    .option('--compute-type <type>', 'gpu or cpu', { choices: ['gpu', 'cpu'], defaultValue: 'gpu' })
    .option('--gpu-type <text>', 'GPU model substring, e.g. mi325x', { complete: gpuTypes('instance') })
    .option('--gpu-count <n>', 'GPUs: 1, 2, 4, or 8', { complete: gpuCounts('instance') })
    .option('--cpu-cores <n>', 'vCPU cores for --compute-type cpu', { complete: cpuCores('instance') })
    .option('--disk <gb>', 'container disk in GB (20-2000)')
    .option('--volume <name:path>', 'mount a network volume, repeatable', { parse: repeat, defaultValue: [], complete: volumeMounts })
    .option('--command <string>', 'start command (whitespace-split, no shell quoting)')
    .option('--env <K=V>', 'environment variable, repeatable', repeat, [])
    .option('--secret <K=V>', 'secret variable, repeatable; replaces the saved secrets', repeat, [])
    .option('--port <port>', 'serve this port at a public HTTPS address, repeatable', repeat, []);
}

/** Only the flags that were typed, so an update changes nothing else. */
export async function templateBody(client: CloudClient, flags: TemplateFlags, cmd: Command, ctx: CliContext): Promise<TemplateBody> {
  const body: TemplateBody = {};

  if (flags.template && flags.image) {
    throw new CliError('Choose either --template or --image.');
  }

  if (flags.description !== undefined) {
    body.description = flags.description;
  }

  if (flags.visibility) {
    body.visibility = flags.visibility as TemplateVisibility;
  }

  if (flags.template) {
    const official = (await client.templates()).filter(template => template.official);
    body.baseTemplateId = resolveTemplateRef(official, flags.template).id;
  }

  if (flags.image) {
    body.containerImage = flags.image.trim();
  }

  if (flags.registry) {
    body.registryCredentialId = resolveCredentialRef(await client.registryCredentials(), flags.registry);
  }

  if (computeFlagsGiven(cmd, flags)) {
    const compute = await resolveCompute(await client.pricing(), {
      computeType: flags.computeType,
      gpuType: flags.gpuType,
      gpuCount: flags.gpuCount,
      cpuCores: flags.cpuCores,
    }, ctx);

    body.computeSku = compute.computeSku;
  }

  if (flags.disk !== undefined) {
    body.ephemeralDiskGiB = parseDisk(flags.disk);
  }

  if (flags.volume.length) {
    const volumes = await client.listVolumes();

    const mounts = parseVolumeFlags(flags.volume).map(entry => ({
      networkVolumeId: resolveVolume(volumes, entry.volume),
      mountPath: entry.mountPath,
    }));

    validateMounts(mounts);
    body.networkVolumeMounts = mounts;
  }

  if (flags.command !== undefined) {
    body.command = flags.command.trim() ? flags.command.trim().split(/\s+/) : [];
  }

  if (flags.env.length) {
    body.environment = toEnvironment(parseRepeatable(flags.env, 'env'));
  }

  if (flags.secret.length) {
    body.secrets = toEnvironment(parseRepeatable(flags.secret, 'secret'));
  }

  if (flags.port.length) {
    body.publicPorts = parsePorts(flags.port);
  }

  return body;
}
