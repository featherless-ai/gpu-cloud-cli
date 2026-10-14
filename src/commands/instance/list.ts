import type { Command } from 'commander';
import type { CliCommand } from '../../completions';
import { profiles } from '../shared';
import { createContext } from '../../context';
import { resolveAuth } from '../../config';
import { CloudClient } from '../../clients/cloud';
import { formatDollars } from '../../version';
import type { InstanceListResponse } from '../../types/instance';

export function registerInstancesListCommand(parent: CliCommand): void {
  parent
    .command('list')
    .description('List GPU instances')
    .option('--page <number>', 'page number', '1')
    .option('--limit <number>', 'instances per page (1-100)', '20')
    .option('--profile <name>', 'profile to use', { complete: profiles })
    .action(async (options: { page: string; limit: string; profile?: string }, cmd: Command) => {
      const ctx = createContext(cmd);
      const auth = await resolveAuth(options.profile);
      const client = new CloudClient(auth.apiBase, auth.apiKey);

      const data = await ctx.spin('Fetching instances', () =>
        client.listInstances({ page: Number(options.page), limit: Number(options.limit) }));

      ctx.output(data, (list: InstanceListResponse) => ({
        table: {
          header: ['NAME', 'ID', 'STATE', 'COMPUTE', 'GPU', 'PRICE/HR', 'CREATED'],
          rows: list.items.map(item => [
            item.name,
            item.shortId,
            item.startupStage ? `${item.state} (${item.startupStage})` : item.state,
            item.computeLabel,
            item.computeType === 'cpu' ? '-' : String(item.gpuCount),
            `${formatDollars(item.costMicrosPerHour)}/hr`,
            item.createdAt.slice(0, 10),
          ]),
        },
        footer: `Page ${list.pagination.page} of ${list.pagination.totalPages} (${list.pagination.totalItems} total)`,
      }));
    });
}
