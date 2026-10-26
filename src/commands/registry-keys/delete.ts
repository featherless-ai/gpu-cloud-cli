import type { Command } from 'commander';
import { CliCommand } from '../../completions';
import { createContext } from '../../context';
import { resolveAuth } from '../../config';
import { CloudClient } from '../../clients/cloud';
import { profiles, registries, requireDeletion, resolveCredentialRef } from '../shared';
import { explainForbidden } from './add';

export const registryKeysDeleteCommand = new CliCommand('delete')
  .argument('<key>', 'registry key name or id', { complete: registries })
  .description('Remove a registry key')
  .option('--yes', 'skip confirmation')
  .option('--profile <name>', 'profile to use', { complete: profiles })
  .action(async (ref: string, options: { yes?: boolean; profile?: string }, cmd: Command) => {
    const ctx = createContext(cmd);
    const auth = await resolveAuth(options.profile);
    const client = new CloudClient(auth.apiBase, auth.apiKey);

    const keys = await ctx.spin('Finding registry key', () => client.registryCredentials());
    const id = resolveCredentialRef(keys, ref);
    const name = keys.find(key => key.id === id)!.name;

    await requireDeletion(ctx, `registry key ${name}`, Boolean(options.yes));
    await ctx.spin('Removing registry key', () => client.deleteRegistryCredential(id)).catch(explainForbidden);

    ctx.success(`Removed registry key ${name}.`);
  });
