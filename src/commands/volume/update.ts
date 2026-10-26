import type { Command } from 'commander';
import { CliCommand } from '../../completions';
import { createContext } from '../../context';
import { resolveAuth } from '../../config';
import { CloudClient } from '../../clients/cloud';
import { CliError } from '../../error';
import { findVolume, profiles, volumes } from '../shared';
import type { NetworkVolumeDto } from '../../types/volume';

function parseCapacity(value: string): number {
  const capacity = Number(value);

  if (!Number.isInteger(capacity) || capacity < 10 || capacity > 10240) {
    throw new CliError('Capacity must be between 10 and 10240 GB.');
  }

  return capacity;
}

export const volumeUpdateCommand = new CliCommand('update')
  .argument('<id>', 'volume name or id', { complete: volumes })
  .description('Rename or grow a network volume')
  .option('--name <new>', 'new volume name')
  .option('--gb <n>', 'new capacity in GB (grow-only, 10-10240)')
  .option('--profile <name>', 'profile to use', { complete: profiles })
  .action(async (id: string, options: { name?: string; gb?: string; profile?: string }, cmd: Command) => {
    const ctx = createContext(cmd);
    const auth = await resolveAuth(options.profile);
    const client = new CloudClient(auth.apiBase, auth.apiKey);

    if (options.name === undefined && options.gb === undefined) {
      ctx.fatal('Nothing to update.');
    }

    const volume = await ctx.spin('Updating volume', async () => {
      const volumes = await client.listVolumes();
      const current = await client.getVolume(findVolume(volumes, id).id);

      const body: { name?: string; capacityGiB?: number } = {};

      if (options.name !== undefined) {
        body.name = options.name;
      }

      if (options.gb !== undefined) {
        const capacity = parseCapacity(options.gb);

        if (capacity < current.capacityGiB) {
          throw new CliError(`Volumes can only grow. Current capacity is ${current.capacityGiB} GB.`);
        }

        body.capacityGiB = capacity;
      }

      return client.patchVolume(current.id, body);
    });

    ctx.success(`Volume ${volume.name} updated.`);
    ctx.output(volume, (item: NetworkVolumeDto) => ({ text: `STATE ${item.state} · ${item.capacityGiB} GiB` }));
  });
