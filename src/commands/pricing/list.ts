import type { Command } from 'commander';
import { CliCommand } from '../../completions';
import { profiles } from '../shared';
import { createContext } from '../../context';
import { resolveAuth } from '../../config';
import { CloudClient } from '../../clients/cloud';
import { formatDollars } from '../../version';
import { computeTypeOf, type PriceRate } from '../../types/pricing';

const UNIT_LABEL: Record<PriceRate['unit'], string> = {
  gpu_hour: '/GPU-hr',
  vcpu_hour: '/vCPU-hr',
  gib_month: '/GiB-mo',
  gib: '/GiB',
};

export const pricingListCommand = new CliCommand('list')
  .description('List current prices')
  .option('--workload <kind>', 'prices for instance or run-job', { choices: ['instance', 'run-job'], defaultValue: 'instance' })
  .option('--all', 'include storage and bandwidth rates')
  .option('--profile <name>', 'profile to use', { complete: profiles })
  .action(async (options: { all?: boolean; profile?: string; workload: 'instance' | 'run-job' }, cmd: Command) => {
    const ctx = createContext(cmd);
    const auth = await resolveAuth(options.profile);
    const client = new CloudClient(auth.apiBase, auth.apiKey);

    const rates = await ctx.spin('Fetching prices', () => client.pricing(options.workload));

    ctx.output(rates, (list: PriceRate[]) => {
      const rows = (options.all ? list : list.filter(rate => computeTypeOf(rate) !== null))
        .map(rate => [
          rate.displayName,
          rate.gpuCount === null ? '-' : String(rate.gpuCount),
          rate.resources ? String(rate.resources.cpuCores) : '-',
          rate.resources ? String(rate.resources.memoryGiB) : '-',
          rate.resources?.gpuMemoryGiB ? String(rate.resources.gpuMemoryGiB) : '-',
          `${formatDollars(rate.unitPriceMicros)}${UNIT_LABEL[rate.unit]}`,
          rate.compareAtUnitPriceMicros ? formatDollars(rate.compareAtUnitPriceMicros) : '-',
        ]);

      return {
        table: {
          header: ['OFFER', 'GPUS', 'vCPU', 'RAM(GiB)', 'GPU-MEM(GiB)', 'PRICE', 'COMPARE AT'],
          rows,
        },
        footer: options.all ? undefined : 'Add --all for storage and bandwidth rates.',
      };
    });
  });
