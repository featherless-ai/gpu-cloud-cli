import type { Command } from 'commander';
import type { CliCommand, Completer } from '../../completions';
import { jobs, profiles, startsWith } from '../shared';
import { CloudClient } from '../../clients/cloud';
import { resolveAuth } from '../../config';
import { createContext } from '../../context';
import { CliError } from '../../error';
import { newContent, nextLogCursor } from '../instance/logs';

const sleep = (ms: number) => new Promise<void>(resolve => setTimeout(resolve, ms));

/** Attempts with recorded output of the job named by the first argument. */
const attempts: Completer = async (word, ctx) => {
  const [id] = ctx.args;

  if (!id) {
    return [];
  }

  const job = await (await ctx.cloud()).getJob(id);

  return job.attempts.filter(attempt => attempt.containerRunId).map(attempt => String(attempt.number)).filter(startsWith(word));
};

export function registerJobLogsCommand(parent: CliCommand): void {
  parent.command('logs')
    .argument('<id>', 'Run Job short id', { complete: jobs })
    .description('Print bounded debug output; use your own logging service for complete logs')
    .option('--head', 'show the captured beginning instead of recent output')
    .option('--tail <n>', 'lines of history (1-5000)', '500')
    .option('--follow', 'keep streaming until the Run Job ends')
    .option('--attempt <n>', 'output of an earlier attempt of a job that was started again', { complete: attempts })
    .option('--profile <name>', 'profile to use', { complete: profiles })
    .action(async (id: string, options: { tail: string; head?: boolean; follow?: boolean; attempt?: string; profile?: string }, cmd: Command) => {
      const ctx = createContext(cmd);

      if (options.head && options.follow) {
        throw new CliError('--head cannot be combined with --follow');
      }

      if (options.attempt !== undefined) {
        if (options.follow) {
          throw new CliError('--attempt cannot be combined with --follow');
        }

        const number = Number(options.attempt);
        const auth = await resolveAuth(options.profile);
        const client = new CloudClient(auth.apiBase, auth.apiKey);
        const job = await ctx.spin('Fetching Run Job', () => client.getJob(id));
        const attempt = job.attempts.find(item => item.number === number);

        if (!attempt) {
          throw new CliError(`Run Job ${job.shortId} has no attempt ${options.attempt}; it has ${job.attempts.length}.`);
        }

        if (!attempt.containerRunId) {
          throw new CliError(`No output was recorded for attempt ${number}.`);
        }

        const runId = attempt.containerRunId;
        const history = await ctx.spin('Fetching logs', () => client.jobLogHistory(job.id, runId, options.head ? 'head' : 'tail'));
        ctx.output(history, result => ({ text: result.text }));
        return;
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
      const first = await ctx.spin('Fetching logs', () => client.jobLogs(id, tail, options.head ? 'head' : 'tail'));
      ctx.output(first, result => ({ text: result.logs }));

      if (!options.follow || first.complete) {
        return;
      }

      let previous = first.logs;
      let terminalObservedAt: number | undefined;

      for (;;) {
        await sleep(2_000);

        const [job, latest] = await Promise.all([
          client.getJob(id).catch(() => null),
          client.jobLogs(id, tail, options.head ? 'head' : 'tail').catch(() => null),
        ]);

        if (latest) {
          const delta = newContent(previous, latest.logs);

          if (delta) {
            process.stdout.write(delta.endsWith('\n') ? delta : `${delta}\n`);
          }

          previous = nextLogCursor(previous, latest.logs, latest.complete);
        }

        if (latest?.complete) {
          ctx.log('logs stopped');
          return;
        }

        if (job && latest && ['succeeded', 'failed', 'cancelled', 'timed_out', 'interrupted'].includes(job.state)) {
          terminalObservedAt ??= Date.now();

          if (Date.now() - terminalObservedAt >= 10_000) {
            ctx.log('Job finished. More output may arrive; run logs again to check.');
            return;
          }
        }

        if (!job && !latest) {
          throw new CliError('Unable to reconnect to the Run Job. Try the command again.');
        }
      }
    });
}
