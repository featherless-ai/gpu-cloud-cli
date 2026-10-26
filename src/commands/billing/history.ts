import type { Command } from 'commander';
import { CliCommand } from '../../completions';
import { profiles } from '../shared';
import { createContext } from '../../context';
import { resolveAuth } from '../../config';
import { CloudClient } from '../../clients/cloud';
import { formatDollars } from '../../version';
import type { LedgerEntry, PaginatedLedger } from '../../types/instance';

const DAY = 10;
const SECONDS_PER_HOUR = 3600;

function entryDate(entry: LedgerEntry): string {
  const start = entry.chargedAt.slice(0, DAY);
  const end = entry.chargedUntil.slice(0, DAY);

  return start === end ? start : `${start}–${end}`;
}

/**
 * Metered lines carry quantity and unit rather than a sentence, so the wording
 * is built here and stays consistent with the console.
 */
function entryDescription(entry: LedgerEntry): string {
  if (!entry.resourceKind) {
    return entry.description;
  }

  const hours = entry.quantity ? (Number(entry.quantity) / SECONDS_PER_HOUR).toFixed(2) : null;
  let measure = entry.skuDisplayName ?? '';

  if (hours && entry.unit === 'gpu_second') {
    measure = `${hours} GPU-hours`;
  } else if (hours && entry.unit === 'vcpu_second') {
    measure = `${hours} vCPU-hours`;
  }

  return [measure, entry.resourceName].filter(Boolean).join(' · ');
}

function amount(micros: string): string {
  const dollars = formatDollars(micros);

  return Number(micros) > 0 ? `+${dollars}` : dollars;
}

export const billingHistoryCommand = new CliCommand('history')
  .description('List charges and top-ups')
  .option('--profile <name>', 'profile to use', { complete: profiles })
  .action(async (options: { profile?: string }, cmd: Command) => {
    const ctx = createContext(cmd);
    const auth = await resolveAuth(options.profile);
    const client = new CloudClient(auth.apiBase, auth.apiKey);

    const ledger = await ctx.spin('Fetching history', () => client.billingLedger());

    ctx.output(ledger, (page: PaginatedLedger) => ({
      table: {
        header: ['DATE', 'TYPE', 'DESCRIPTION', 'AMOUNT'],
        rows: page.items.map(entry => [
          entryDate(entry),
          entry.type,
          entryDescription(entry),
          amount(entry.amountMicros),
        ]),
      },
      footer: `${page.items.length} of ${page.pagination.totalItems} entry(s)`,
    }));
  });
