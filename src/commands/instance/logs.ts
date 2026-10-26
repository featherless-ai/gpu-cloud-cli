import type { Command } from 'commander';
import { CliCommand } from '../../completions';
import { createContext } from '../../context';
import { resolveAuth } from '../../config';
import { CloudClient } from '../../clients/cloud';
import { CliError } from '../../error';
import { instances, resolveInstanceId, profiles } from '../shared';

const POLL_MS = 5_000;

/** Lines of `next` not already emitted from the previous tail slice. */
export function newContent(previous: string, next: string): string {
  if (previous === next) {
    return '';
  }

  if (next.startsWith(previous)) {
    return next.slice(previous.length);
  }

  // The server returns a bounded tail slice: old lines fell off, or the
  // container restarted. Find the longest overlap between next's head and
  // previous's tail so shared lines are never re-emitted.
  const previousLines = previous ? previous.split('\n') : [];
  const nextLines = next.split('\n');
  const max = Math.min(previousLines.length, nextLines.length);

  for (let overlap = max; overlap > 0; overlap -= 1) {
    const head = nextLines.slice(0, overlap).join('\n');
    const tail = previousLines.slice(previousLines.length - overlap).join('\n');

    if (head === tail) {
      return nextLines.slice(overlap).join('\n');
    }
  }

  return next;
}

/** Keep overlap state while live logs hand off to final retained logs. */
export function nextLogCursor(previous: string, next: string, complete: boolean): string {
  return next || complete || !previous ? next : previous;
}

const sleep = (ms: number): Promise<void> => {
  const { promise, resolve } = Promise.withResolvers<void>();
  setTimeout(resolve, ms);

  return promise;
};

export const instanceLogsCommand = new CliCommand('logs')
  .argument('<id>', 'instance name or short id', { complete: instances })
  .description('Print bounded debug output; use your own logging service for complete logs')
  .option('--head', 'show the captured beginning instead of recent output')
  .option('--tail <n>', 'lines of history (1-5000)', '500')
  .option('--follow', 'keep streaming new lines')
  .option('--profile <name>', 'profile to use', { complete: profiles })
  .action(async (rawId: string, options: { tail: string; head?: boolean; follow?: boolean; profile?: string }, cmd: Command) => {
    const ctx = createContext(cmd);

    if (options.head && options.follow) {
      throw new CliError('--head cannot be combined with --follow');
    }

    const tail = Number(options.tail);

    if (!Number.isInteger(tail) || tail < 1 || tail > 5000) {
      throw new CliError('--tail must be between 1 and 5000');
    }

    if (options.follow && ctx.mode === 'json') {
      ctx.fatal('--follow cannot be combined with --json');
    }

    const auth = await resolveAuth(options.profile);
    const client = new CloudClient(auth.apiBase, auth.apiKey);
    const id = await resolveInstanceId(client, rawId);

    const first = await ctx.spin('Fetching logs', () => client.instanceLogs(id, tail, options.head ? 'head' : 'tail'));
    ctx.output(first, result => ({ text: result.logs }));

    if (!options.follow) {
      return;
    }

    let previous = first.logs;

    for (;;) {
      await sleep(POLL_MS);
      const instance = await client.getInstance(id).catch(() => null);
      const latest = await client.instanceLogs(id, tail, options.head ? 'head' : 'tail').catch(() => null);

      if (latest) {
        const delta = newContent(previous, latest.logs);

        if (delta) {
          process.stdout.write(delta.endsWith('\n') ? delta : `${delta}\n`);
        }

        previous = latest.logs;
      }

      if (!instance || instance.state !== 'running') {
        ctx.log('logs stopped');

        return;
      }
    }
  });
