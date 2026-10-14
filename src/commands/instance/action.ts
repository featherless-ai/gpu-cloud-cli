import type { Command } from 'commander';
import type { CliCommand } from '../../completions';
import { createContext } from '../../context';
import { resolveAuth } from '../../config';
import { CloudClient, type InstanceAction } from '../../clients/cloud';
import { instances, requireDeletion, resolveInstanceId, profiles } from '../shared';

const PAST_TENSE: Record<InstanceAction, string> = {
  start: 'started',
  stop: 'stopped',
  restart: 'restarted',
  delete: 'deleted',
};

const PROGRESS_LABEL: Record<InstanceAction, string> = {
  start: 'Starting',
  stop: 'Stopping',
  restart: 'Restarting',
  delete: 'Deleting',
};

const DESCRIPTION: Record<InstanceAction, string> = {
  start: 'Start',
  stop: 'Stop',
  restart: 'Restart',
  delete: 'Delete',
};

export function registerInstanceActionCommands(parent: CliCommand): void {
  for (const action of ['start', 'stop', 'restart', 'delete'] as const) {
    const command = parent
      .command(action)
      .argument('<id>', 'instance name or short id', { complete: instances })
      .description(`${DESCRIPTION[action]} an instance`)
      .option('--profile <name>', 'profile to use', { complete: profiles })
      .action(async (rawId: string, options: { profile?: string; yes?: boolean }, cmd: Command) => {
        const ctx = createContext(cmd);
        const auth = await resolveAuth(options.profile);
        const client = new CloudClient(auth.apiBase, auth.apiKey);
        const id = await resolveInstanceId(client, rawId);

        if (action === 'delete') {
          await requireDeletion(ctx, `instance ${rawId}`, Boolean(options.yes));
        }

        const instance = await ctx.spin(`${PROGRESS_LABEL[action]} instance`, () =>
          client.instanceAction(id, action));

        if (action === 'delete') {
          ctx.success(`Instance deletion started for ${instance.name} (${instance.shortId}).`);
        } else {
          ctx.success(`Instance ${instance.name} ${PAST_TENSE[action]}.`);
        }

        ctx.output(instance, item => ({ text: `STATE ${item.state}` }));
      });

    if (action === 'delete') {
      command.option('--yes', 'skip confirmation');
    }
  }
}
