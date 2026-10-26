import type { Command } from 'commander';
import { CliCommand } from '../../completions';
import { CloudClient } from '../../clients/cloud';
import { resolveAuth } from '../../config';
import { createContext } from '../../context';
import { jobs, requireDeletion, profiles } from '../shared';

export const jobCancelCommand = new CliCommand('cancel')
  .argument('<id>', 'Run Job short id', { complete: jobs })
  .description('Cancel a queued or running Run Job')
  .option('--profile <name>', 'profile to use', { complete: profiles })
  .action(async (id: string, options: { profile?: string }, cmd: Command) => {
    const ctx = createContext(cmd);
    const auth = await resolveAuth(options.profile);
    const job = await ctx.spin('Cancelling Run Job', () => new CloudClient(auth.apiBase, auth.apiKey).cancelJob(id));
    ctx.success(`Cancellation started for ${job.name} (${job.shortId}).`);
    ctx.output(job, item => ({ text: `STATE ${item.state}` }));
  });

export const jobDeleteCommand = new CliCommand('delete')
  .argument('<id>', 'Run Job short id', { complete: jobs })
  .description('Delete a Run Job record')
  .option('--yes', 'skip confirmation')
  .option('--profile <name>', 'profile to use', { complete: profiles })
  .action(async (id: string, options: { yes?: boolean; profile?: string }, cmd: Command) => {
    const ctx = createContext(cmd);
    const auth = await resolveAuth(options.profile);
    await requireDeletion(ctx, `Run Job ${id}`, Boolean(options.yes));
    const job = await ctx.spin('Deleting Run Job', () => new CloudClient(auth.apiBase, auth.apiKey).deleteJob(id));
    ctx.success(`Run Job deletion started for ${job.name} (${job.shortId}).`);
    ctx.output(job, item => ({ text: `STATE ${item.state}` }));
  });
