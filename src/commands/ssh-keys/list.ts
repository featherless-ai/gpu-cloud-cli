import type { Command } from 'commander';
import { CliCommand } from '../../completions';
import { profiles } from '../shared';
import { createContext } from '../../context';
import { resolveAuth } from '../../config';
import { CloudClient } from '../../clients/cloud';
import type { SshKey } from '../../types/instance';

export const sshKeysListCommand = new CliCommand('list')
  .description('List your SSH public keys')
  .option('--profile <name>', 'profile to use', { complete: profiles })
  .action(async (options: { profile?: string }, cmd: Command) => {
    const ctx = createContext(cmd);
    const auth = await resolveAuth(options.profile);
    const client = new CloudClient(auth.apiBase, auth.apiKey);

    const keys = await ctx.spin('Fetching SSH keys', () => client.sshKeys());

    ctx.output(keys, (list: SshKey[]) => {
      if (list.length === 0) {
        return { text: 'No SSH keys. Add one with featherless-cloud ssh-keys add.' };
      }

      return {
        table: {
          header: ['NAME', 'FINGERPRINT', 'ID', 'ADDED'],
          rows: list.map(key => [key.name, key.fingerprint, key.id, key.createdAt.slice(0, 10)]),
        },
      };
    });
  });
