import type { Command } from 'commander';
import { CliCommand } from '../../completions';
import { createContext } from '../../context';
import { resolveAuth } from '../../config';
import { CloudClient } from '../../clients/cloud';
import { profiles, requireDeletion, resolveSshKeyRef, sshKeys } from '../shared';

export const sshKeysDeleteCommand = new CliCommand('delete')
  .argument('<key>', 'SSH key name or id', { complete: sshKeys })
  .description('Remove an SSH public key')
  .option('--yes', 'skip confirmation')
  .option('--profile <name>', 'profile to use', { complete: profiles })
  .action(async (ref: string, options: { yes?: boolean; profile?: string }, cmd: Command) => {
    const ctx = createContext(cmd);
    const auth = await resolveAuth(options.profile);
    const client = new CloudClient(auth.apiBase, auth.apiKey);

    const keys = await ctx.spin('Finding SSH key', () => client.sshKeys());
    const key = resolveSshKeyRef(keys, ref);

    await requireDeletion(ctx, `SSH key ${key.name}`, Boolean(options.yes));
    await ctx.spin('Removing SSH key', () => client.deleteSshKey(key.id));

    ctx.success(`Removed SSH key ${key.name}.`);
  });
