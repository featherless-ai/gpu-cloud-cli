import type { Command } from 'commander';
import { CliCommand } from '../../completions';
import { profiles } from '../shared';
import { createContext } from '../../context';
import { resolveAuth, consoleBaseUrl } from '../../config';
import { CloudClient } from '../../clients/cloud';
import { formatDollars } from '../../version';
import type { BillingSummary } from '../../types/instance';

export const billingSettingsCommand = new CliCommand('settings')
  .description('Show balance and reload settings')
  .option('--profile <name>', 'profile to use', { complete: profiles })
  .action(async (options: { profile?: string }, cmd: Command) => {
    const ctx = createContext(cmd);
    const auth = await resolveAuth(options.profile);
    const client = new CloudClient(auth.apiBase, auth.apiKey);

    const summary = await ctx.spin('Fetching billing', () => client.billingSummary());

    ctx.output(summary, (item: BillingSummary) => ({
      text: [
        `Balance       ${formatDollars(item.balanceMicros)} (${item.currency})`,
        `Auto-reload   ${item.autoReload.enabled ? 'enabled' : 'disabled'}`,
      ].join('\n'),
    }));

    ctx.log(`Add credit at ${consoleBaseUrl()}/billing`);
  });
