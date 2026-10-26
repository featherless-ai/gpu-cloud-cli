import type { Command } from 'commander';
import { CliCommand } from '../../completions';
import { profiles } from '../shared';
import { createContext } from '../../context';
import { resolveAuth } from '../../config';
import { CloudClient } from '../../clients/cloud';
import { CliError } from '../../error';
import type { NetworkVolumeDto } from '../../types/volume';

const WAIT_POLL_MS = 1_000;
const WAIT_MAX_POLLS = 30;

const sleep = (ms: number): Promise<void> => {
  const { promise, resolve } = Promise.withResolvers<void>();
  setTimeout(resolve, ms);

  return promise;
};

function parseCapacity(value: string | undefined): number {
  if (value === undefined) {
    return 100;
  }

  const capacity = Number(value);

  if (!Number.isInteger(capacity) || capacity < 10 || capacity > 10240) {
    throw new CliError('Capacity must be between 10 and 10240 GB.');
  }

  return capacity;
}

export const volumeCreateCommand = new CliCommand('create')
  .argument('<name>')
  .description('Create a network volume')
  .option('--gb <n>', 'capacity in GB (10-10240)', '100')
  .option('--wait', 'wait until the volume is usable')
  .option('--profile <name>', 'profile to use', { complete: profiles })
  .action(async (name: string, options: { gb: string; wait?: boolean; profile?: string }, cmd: Command) => {
    const ctx = createContext(cmd);
    const auth = await resolveAuth(options.profile);
    const client = new CloudClient(auth.apiBase, auth.apiKey);

    const volume = await ctx.spin('Creating volume', () =>
      client.createVolume({ name, capacityGiB: parseCapacity(options.gb) }, crypto.randomUUID()));

    let final = volume;

    if (options.wait) {
      for (let attempt = 0; attempt < WAIT_MAX_POLLS; attempt += 1) {
        if (final.state === 'available' || final.state === 'in_use') {
          break;
        }

        if (final.state === 'error') {
          break;
        }

        await sleep(WAIT_POLL_MS);
        final = await client.getVolume(volume.id);
      }
    }

    if (final.state === 'error') {
      ctx.fatal(`Volume ${final.name} failed to provision: ${final.lastError ?? 'unknown error'}`);
    }

    if (final.state === 'available' || final.state === 'in_use') {
      ctx.success(`Network volume ${final.name} is ready.`);
    } else if (options.wait) {
      ctx.warn(`Volume ${final.name} is still ${final.state}. Check with featherless-cloud volume get ${final.id}.`);
    } else {
      ctx.success(`Network volume ${final.name} is provisioning. It becomes usable automatically — check with featherless-cloud volume get.`);
    }

    ctx.output(final, (item: NetworkVolumeDto) => ({ text: `STATE ${item.state}` }));
  });
