import type { Command } from 'commander';
import { CliCommand } from '../../completions';
import { createContext } from '../../context';
import { resolveAuth } from '../../config';
import { CloudClient, type RegistryCredentialBody } from '../../clients/cloud';
import { CliError } from '../../error';
import { profiles, registries, resolveCredentialRef } from '../shared';
import type { RegistryCredential } from '../../types/instance';
import { explainForbidden, readSecret } from './add';

interface UpdateOptions {
  name?: string;
  server?: string;
  username?: string;
  changePassword?: boolean;
  passwordStdin?: boolean;
  profile?: string;
}

export const registryKeysUpdateCommand = new CliCommand('update')
  .argument('<key>', 'registry key name or id', { complete: registries })
  .description('Change a registry key')
  .option('--name <name>', 'new name')
  .option('--server <host>', 'new registry host')
  .option('--username <user>', 'new registry username')
  .option('--change-password', 'prompt for a new password or access token')
  .option('--password-stdin', 'read a new password or access token from stdin')
  .option('--profile <name>', 'profile to use', { complete: profiles })
  .action(async (ref: string, options: UpdateOptions, cmd: Command) => {
    const ctx = createContext(cmd);
    const body: Partial<RegistryCredentialBody> = {};

    if (options.name) {
      body.name = options.name;
    }

    if (options.server) {
      body.registry = options.server;
    }

    if (options.username) {
      body.username = options.username;
    }

    if (options.changePassword || options.passwordStdin) {
      body.secret = await readSecret(ctx, Boolean(options.passwordStdin));
    }

    if (Object.keys(body).length === 0) {
      throw new CliError('Nothing to change. Pass --name, --server, --username, --change-password or --password-stdin.');
    }

    const auth = await resolveAuth(options.profile);
    const client = new CloudClient(auth.apiBase, auth.apiKey);

    const keys = await ctx.spin('Finding registry key', () => client.registryCredentials());
    const id = resolveCredentialRef(keys, ref);

    const key = await ctx.spin('Updating registry key', () => client.updateRegistryCredential(id, body))
      .catch(explainForbidden);

    ctx.success(`Updated registry key ${key.name}.`);
    ctx.output(key, (item: RegistryCredential) => ({ text: item.id }));
  });
