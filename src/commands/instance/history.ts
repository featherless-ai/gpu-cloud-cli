import type { Command } from 'commander';
import type { CliCommand } from '../../completions';
import { createContext } from '../../context';
import { resolveAuth } from '../../config';
import { CloudClient } from '../../clients/cloud';
import { instances, resolveInstanceId, profiles } from '../shared';
import type { InstanceEvent } from '../../types/instance';

function truncate(message: string, max = 80): string {
  const single = message.replace(/\s+/g, ' ').trim();

  return single.length > max ? `${single.slice(0, max - 1)}…` : single;
}

export function registerInstanceHistoryCommand(parent: CliCommand): void {
  parent
    .command('history')
    .argument('<id>', 'instance name or short id', { complete: instances })
    .description('Show instance lifecycle events')
    .option('--profile <name>', 'profile to use', { complete: profiles })
    .action(async (rawId: string, options: { profile?: string }, cmd: Command) => {
      const ctx = createContext(cmd);
      const auth = await resolveAuth(options.profile);
      const client = new CloudClient(auth.apiBase, auth.apiKey);
      const id = await resolveInstanceId(client, rawId);

      const events = await ctx.spin('Fetching history', () => client.instanceHistory(id));

      ctx.output(events, (list: InstanceEvent[]) => ({
        table: {
          header: ['TYPE', 'REASON', 'COUNT', 'LAST SEEN', 'MESSAGE'],
          rows: [...list]
            .sort((a, b) => a.lastObservedAt.localeCompare(b.lastObservedAt))
            .map(event => [
              event.type,
              event.reason,
              String(event.count),
              event.lastObservedAt.replace('T', ' ').slice(0, 19),
              truncate(event.message),
            ]),
        },
        footer: `${list.length} event(s)`,
      }));
    });
}
