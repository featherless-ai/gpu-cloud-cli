import type { Command } from 'commander';
import { CliCommand } from '../../completions';
import { createContext } from '../../context';
import { resolveAuth } from '../../config';
import { CloudClient } from '../../clients/cloud';
import { findVolume, requireDeletion, profiles, volumes } from '../shared';

export const volumeDeleteCommand = new CliCommand('delete')
  .argument('<id>', 'volume name or id', { complete: volumes })
  .description('Delete a network volume')
  .option('--yes', 'skip confirmation')
  .option('--profile <name>', 'profile to use', { complete: profiles })
  .action(async (id: string, options: { yes?: boolean; profile?: string }, cmd: Command) => {
    const ctx = createContext(cmd);
    const auth = await resolveAuth(options.profile);
    const client = new CloudClient(auth.apiBase, auth.apiKey);

    const volume = await ctx.spin('Finding volume', async () => {
      const volumes = await client.listVolumes();
      return client.getVolume(findVolume(volumes, id).id);
    });

    const attached = volume.attachments;

    if (attached.length) {
      const list = attached.map(attachment => `  ${attachment.name}  ${attachment.mountPath}`).join('\n');
      ctx.warn(`Attached to:\n${list}\nDeleting detaches it from these instances, which must be stopped. They'll start without it, and files written to those paths won't be kept.`);
    }

    await requireDeletion(ctx, `volume ${volume.name}`, Boolean(options.yes));
    await ctx.spin('Deleting volume', () => client.deleteVolume(volume.id, attached.length > 0));

    ctx.success(`Volume deletion started for ${volume.name}.`);
  });
