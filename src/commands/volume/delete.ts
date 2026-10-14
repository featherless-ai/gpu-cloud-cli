import type { Command } from 'commander';
import type { CliCommand } from '../../completions';
import { createContext } from '../../context';
import { resolveAuth } from '../../config';
import { CloudClient } from '../../clients/cloud';
import { findVolume, requireDeletion, profiles, volumes } from '../shared';

export function registerVolumeDeleteCommand(parent: CliCommand): void {
  parent
    .command('delete')
    .argument('<id>', 'volume name or id', { complete: volumes })
    .description('Delete a network volume')
    .option('--yes', 'skip confirmation')
    .option('--profile <name>', 'profile to use', { complete: profiles })
    .action(async (id: string, options: { yes?: boolean; profile?: string }, cmd: Command) => {
      const ctx = createContext(cmd);
      const auth = await resolveAuth(options.profile);
      const client = new CloudClient(auth.apiBase, auth.apiKey);

      await requireDeletion(ctx, `volume ${id}`, Boolean(options.yes));

      const volume = await ctx.spin('Deleting volume', async () => {
        const volumes = await client.listVolumes();
        const current = await client.getVolume(findVolume(volumes, id).id);
        await client.deleteVolume(current.id);

        return current;
      });

      ctx.success(`Volume deletion started for ${volume.name}.`);
    });
}
