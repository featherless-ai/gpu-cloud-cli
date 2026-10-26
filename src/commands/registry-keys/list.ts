import type { Command } from 'commander';
import { CliCommand } from '../../completions';
import { profiles } from '../shared';
import { createContext } from '../../context';
import { resolveAuth } from '../../config';
import { CloudClient } from '../../clients/cloud';
import type { RegistryCredential } from '../../types/instance';

export const registryKeysListCommand = new CliCommand('list')
  .description('List registry keys')
  .option('--profile <name>', 'profile to use', { complete: profiles })
  .action(async (options: { profile?: string }, cmd: Command) => {
    const ctx = createContext(cmd);
    const auth = await resolveAuth(options.profile);
    const client = new CloudClient(auth.apiBase, auth.apiKey);

    const keys = await ctx.spin('Fetching registry keys', () => client.registryCredentials());

    ctx.output(keys, (list: RegistryCredential[]) => {
      if (list.length === 0) {
        return { text: 'No registry keys. Add one with featherless-cloud registry-keys add.' };
      }

      return {
        table: {
          header: ['NAME', 'SERVER', 'USERNAME', 'ID', 'UPDATED'],
          rows: list.map(key => [key.name, key.registry, key.username, key.id, key.updatedAt.slice(0, 10)]),
        },
      };
    });
  });
