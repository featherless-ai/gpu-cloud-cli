import type { Command } from 'commander';
import type { CliCommand } from '../../completions';
import { profiles } from '../shared';
import { CloudClient } from '../../clients/cloud';
import { resolveAuth } from '../../config';
import { createContext } from '../../context';
import type { RunJobListResponse } from '../../types/job';
import { formatDollars } from '../../version';

export function registerJobListCommand(parent: CliCommand): void {
  parent.command('list')
    .description('List Run Jobs')
    .option('--page <number>', 'page number', '1')
    .option('--limit <number>', 'Run Jobs per page (1-100)', '20')
    .option('--profile <name>', 'profile to use', { complete: profiles })
    .action(async (options: { page: string; limit: string; profile?: string }, cmd: Command) => {
      const ctx = createContext(cmd);
      const auth = await resolveAuth(options.profile);

      const data = await ctx.spin('Fetching Run Jobs', () => new CloudClient(auth.apiBase, auth.apiKey)
        .listJobs({ page: Number(options.page), limit: Number(options.limit) }));

      ctx.output(data, (list: RunJobListResponse) => ({
        table: {
          header: ['NAME', 'ID', 'STATE', 'COMPUTE', 'EXIT', 'PRICE/HR', 'CREATED'],
          rows: list.items.map(item => [
            item.name,
            item.shortId,
            item.admissionState === 'waiting' ? 'waiting' : item.state,
            item.computeType === 'gpu'
              ? `${item.gpuCount} x ${item.resources.gpuModel ?? item.computeSku}`
              : `${item.resources.cpuCores} vCPU`,
            item.exitCode === null ? '-' : String(item.exitCode),
            `${formatDollars(item.costMicrosPerHour)}/hr`,
            item.createdAt.slice(0, 10),
          ]),
        },
        footer: `Page ${list.pagination.page} of ${list.pagination.totalPages} (${list.pagination.totalItems} total)`,
      }));
    });
}
