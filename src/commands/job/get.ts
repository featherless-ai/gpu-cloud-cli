import type { Command } from 'commander';
import type { CliCommand } from '../../completions';
import { jobs, profiles } from '../shared';
import { CloudClient } from '../../clients/cloud';
import { resolveAuth } from '../../config';
import { createContext } from '../../context';
import type { RunJobDto } from '../../types/job';
import { formatDollars } from '../../version';

/** Why a queued Run Job has not been admitted, in words; anything else is just waiting. */
const WAITING_LABELS: Record<string, string> = {
  queue_credit: 'waiting for credit',
  queue_quota: 'waiting for organization limit',
  queue_capacity: 'waiting for cluster capacity',
};

export function registerJobGetCommand(parent: CliCommand): void {
  parent.command('get')
    .argument('<id>', 'Run Job short id', { complete: jobs })
    .description('Show one Run Job')
    .option('--profile <name>', 'profile to use', { complete: profiles })
    .action(async (id: string, options: { profile?: string }, cmd: Command) => {
      const ctx = createContext(cmd);
      const auth = await resolveAuth(options.profile);
      const job = await ctx.spin('Fetching Run Job', () => new CloudClient(auth.apiBase, auth.apiKey).getJob(id));

      if (job.statusMessage) {
        ctx.warn(job.statusMessage);
      }

      ctx.output(job, (item: RunJobDto) => ({
        text: [
          `NAME        ${item.name}`,
          `SHORT ID    ${item.shortId}`,
          `STATE       ${item.admissionState === 'waiting' ? WAITING_LABELS[item.statusReason ?? ''] ?? 'waiting' : item.state}`,
          `COMPUTE     ${item.computeType === 'gpu' ? `${item.gpuCount} x ${item.resources.gpuModel ?? item.computeSku}` : `${item.resources.cpuCores} vCPU / ${item.resources.memoryGiB} GiB`}`,
          `HOURLY      ${formatDollars(item.costMicrosPerHour)}/hr`,
          `IMAGE       ${item.image.reference}`,
          `COMMAND     ${item.command.join(' ')}`,
          `TIMEOUT     ${item.timeoutSeconds}s after compute assignment`,
          `EXIT CODE   ${item.exitCode ?? '-'}`,
          `RETRIES     ${item.preemptionRetries} if interrupted for other work`,
          ...(item.attempts.length > 1 ? item.attempts.map(attempt => `ATTEMPT ${attempt.number}   ${attempt.state}${attempt.startedAt ? `, started ${attempt.startedAt}` : ''}`) : []),
          ...item.networkVolumeMounts.map(mount => `VOLUME      ${mount.name} → ${mount.mountPath}`),
          `CREATED     ${item.createdAt}`,
        ].join('\n'),
      }));
    });
}
