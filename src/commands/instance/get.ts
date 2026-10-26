import type { Command } from 'commander';
import { CliCommand } from '../../completions';
import { createContext } from '../../context';
import { resolveAuth } from '../../config';
import { instances, resolveInstanceId, profiles } from '../shared';
import { CloudClient } from '../../clients/cloud';
import { formatDollars } from '../../version';
import type { InstanceDto } from '../../types/instance';

/** Elapsed wall time since the container started, console-parity format. */
function uptime(runtimeStartedAt: string | null | undefined): string | null {
  if (!runtimeStartedAt) {
    return null;
  }

  const elapsedMinutes = Math.max(0, Math.floor((Date.now() - new Date(runtimeStartedAt).getTime()) / 60_000));
  const days = Math.floor(elapsedMinutes / 1_440);
  const hours = Math.floor((elapsedMinutes % 1_440) / 60);
  const minutes = elapsedMinutes % 60;

  if (days) {
    return `${days}d ${hours}h`;
  }

  if (hours) {
    return `${hours}h ${minutes}m`;
  }

  return elapsedMinutes ? `${elapsedMinutes}m` : '<1m';
}

function stateLabel(instance: InstanceDto): string {
  return instance.startupStage ? `${instance.state} (${instance.startupStage})` : instance.state;
}

function computeLabel(instance: InstanceDto): string {
  const resources = instance.resources;

  if (!resources) {
    return instance.computeLabel;
  }

  if (instance.computeType === 'gpu') {
    return `${instance.gpuCount} x ${resources.gpuModel ?? instance.computeLabel}`;
  }

  return `${resources.cpuCores} cores / ${resources.memoryGiB} GiB`;
}

export const instanceGetCommand = new CliCommand('get')
  .argument('<id>', 'instance name or short id', { complete: instances })
  .description('Show one instance')
  .option('--profile <name>', 'profile to use', { complete: profiles })
  .action(async (rawId: string, options: { profile?: string }, cmd: Command) => {
    const ctx = createContext(cmd);
    const auth = await resolveAuth(options.profile);
    const client = new CloudClient(auth.apiBase, auth.apiKey);
    const id = await resolveInstanceId(client, rawId);

    const instance = await ctx.spin('Fetching instance', () => client.getInstance(id));

    if (instance.lastError) {
      ctx.warn(instance.lastError);
    }

    ctx.output(instance, (item: InstanceDto) => {
      const lines: string[] = [
        `NAME        ${item.name}`,
        `SHORT ID    ${item.shortId}`,
        `STATE       ${stateLabel(item)}`,
        `HOURLY      ${formatDollars(item.costMicrosPerHour)}/hr`,
        `COMPUTE     ${computeLabel(item)}`,
        `DISK        ${item.ephemeralDiskGiB} GiB container`,
      ];

      for (const mount of item.networkVolumeMounts ?? []) {
        lines.push(`VOLUMES     ${mount.volume.name} → ${mount.mountPath}`);
      }

      for (const port of item.publicPorts ?? []) {
        lines.push(`PORT        ${port.port} → ${port.url ?? 'unavailable'}`);
      }

      if (item.image) {
        lines.push(`IMAGE       ${item.image.reference}`);
      }

      if (item.templateVersion) {
        lines.push(`TEMPLATE    ${item.templateVersion.name} ${item.templateVersion.version}`);
      }

      if (item.command) {
        lines.push(`COMMAND     ${item.command.length ? item.command.join(' ') : 'Template default'}`);
      }

      for (const variable of item.environment ?? []) {
        lines.push(`ENVIRONMENT ${variable.name}=${variable.value}`);
      }

      if ((item.secretNames?.length ?? 0) > 0) {
        lines.push(`SECRETS     ${item.secretNames?.length} secret(s) redacted`);
      }

      const up = uptime(item.runtimeStartedAt);

      if (up) {
        lines.push(`UPTIME      ${up}`);
      }

      lines.push(`CREATED     ${item.createdAt.slice(0, 10)}`);

      return { text: lines.join('\n') };
    });
  });
