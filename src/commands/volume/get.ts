import type { Command } from 'commander';
import type { CliCommand } from '../../completions';
import { createContext } from '../../context';
import { resolveAuth } from '../../config';
import { CloudClient } from '../../clients/cloud';
import { findVolume, profiles, volumes } from '../shared';
import { formatDollars } from '../../version';
import type { NetworkVolumeDto } from '../../types/volume';

export function registerVolumeGetCommand(parent: CliCommand): void {
  parent
    .command('get')
    .argument('<id>', 'volume name or id', { complete: volumes })
    .description('Show one network volume')
    .option('--profile <name>', 'profile to use', { complete: profiles })
    .action(async (id: string, options: { profile?: string }, cmd: Command) => {
      const ctx = createContext(cmd);
      const auth = await resolveAuth(options.profile);
      const client = new CloudClient(auth.apiBase, auth.apiKey);

      const volume = await ctx.spin('Fetching volume', async () => {
        const volumes = await client.listVolumes();

        return client.getVolume(findVolume(volumes, id).id);
      });

      if (volume.lastError) {
        ctx.warn(volume.lastError);
      }

      ctx.output(volume, (item: NetworkVolumeDto) => {
        const lines: string[] = [
          `NAME        ${item.name}`,
          `ID          ${item.id}`,
          `STATE       ${item.state}`,
          `CAPACITY    ${item.capacityGiB} GiB`,
          `PRICE       ${formatDollars(item.pricing.unitPriceMicrosPerGiBMonth)} / GiB / month`,
          `EST/MONTH   ${formatDollars(item.pricing.estimatedMicrosPerMonth)} / month`,
        ];

        for (const attachment of item.attachments) {
          lines.push(`ATTACHED TO ${attachment.name} (${attachment.shortId}) → ${attachment.mountPath}`);
        }

        lines.push(`CREATED     ${item.createdAt.slice(0, 10)}`);

        return { text: lines.join('\n') };
      });
    });
}
