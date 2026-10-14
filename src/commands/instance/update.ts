import type { Command } from 'commander';
import type { CliCommand } from '../../completions';
import { createContext } from '../../context';
import { resolveAuth } from '../../config';
import { CloudClient, type UpdateInstanceBody } from '../../clients/cloud';
import { CliError } from '../../error';
import {
  assertInstanceName, instances, parsePorts, parseRepeatable, profiles, registries, resolveCredentialRef, resolveInstanceId, toEnvironment,
} from '../shared';

interface UpdateOptions {
  name?: string;
  image?: string;
  registry?: string;
  disk?: string;
  command?: string;
  env?: string[];
  port?: string[];
  profile?: string;
}

function parseDisk(value: string): number {
  const disk = Number(value);

  if (!Number.isInteger(disk) || disk < 20 || disk > 2000) {
    throw new CliError('Container disk must be between 20 and 2000 GB.');
  }

  return disk;
}

export function registerInstanceUpdateCommand(parent: CliCommand): void {
  parent
    .command('update')
    .argument('<id>', 'instance name or short id', { complete: instances })
    .description('Update an instance (config fields require a stopped instance)')
    .option('--name <name>', 'rename the instance')
    .option('--image <reference>', 'container image (requires stopped state)')
    .option('--registry <name|id|null>', 'registry credential, or "null" for public pulls', {
      complete: async (word, ctx) => [...await registries(word, ctx), ...['null'].filter(value => value.startsWith(word))],
    })
    .option('--disk <gb>', 'container disk in GB, 20-2000 (requires stopped state)')
    .option('--command <string>', 'start command, whitespace-split (requires stopped state)')
    .option('--env <K=V>', 'environment variable, repeatable — replaces the whole environment', (v: string, prev: string[]) => [...prev, v], [])
    .option('--port <port|none>', 'public port, repeatable — replaces every public port, applied at once; "none" removes them', (v: string, prev: string[]) => [...prev, v], [])
    .option('--profile <name>', 'profile to use', { complete: profiles })
    .action(async (rawId: string, options: UpdateOptions, cmd: Command) => {
      const ctx = createContext(cmd);
      const auth = await resolveAuth(options.profile);
      const client = new CloudClient(auth.apiBase, auth.apiKey);
      const id = await resolveInstanceId(client, rawId);

      const body: UpdateInstanceBody = {};

      if (options.name !== undefined) {
        assertInstanceName(options.name);
        body.name = options.name;
      }

      if (options.image !== undefined) {
        const reference = options.image.trim();

        if (reference.length < 3) {
          throw new CliError('Enter a container image.');
        }

        body.containerImage = reference;
      }

      if (options.registry !== undefined) {
        if (options.registry === 'null') {
          body.registryCredentialId = null;
        } else {
          const credentials = await client.registryCredentials();
          body.registryCredentialId = resolveCredentialRef(credentials, options.registry);
        }
      }

      if (options.disk !== undefined) {
        body.ephemeralDiskGiB = parseDisk(options.disk);
      }

      if (options.command !== undefined) {
        body.command = options.command.trim() ? options.command.trim().split(/\s+/) : [];
      }

      if (options.env?.length) {
        body.environment = toEnvironment(parseRepeatable(options.env, 'env'));
      }

      if (options.port?.length) {
        body.publicPorts = parsePorts(options.port);
      }

      if (Object.keys(body).length === 0) {
        ctx.fatal('Nothing to update.');
      }

      const instance = await ctx.spin('Updating instance', () => client.patchInstance(id, body));

      ctx.success(`Instance ${instance.name} (${instance.shortId}) updated.`);
      ctx.output(instance, item => ({ text: `STATE ${item.state}` }));
    });
}
