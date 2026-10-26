import * as p from '@clack/prompts';
import type { Command } from 'commander';
import { CliCommand } from '../../completions';
import type { CliContext } from '../../context';
import { createContext } from '../../context';
import { resolveAuth } from '../../config';
import { CloudClient } from '../../clients/cloud';
import { CliError, ForbiddenError } from '../../error';
import { profiles } from '../shared';
import type { RegistryCredential } from '../../types/instance';

/** Reads the registry password from stdin, or prompts for it; never from a flag, so it stays out of shell history. */
export async function readSecret(ctx: CliContext, fromStdin: boolean): Promise<string> {
  if (fromStdin) {
    const chunks: Buffer[] = [];

    for await (const chunk of process.stdin) {
      chunks.push(Buffer.from(chunk));
    }

    const secret = Buffer.concat(chunks).toString('utf8').replace(/\r?\n$/, '');

    if (!secret) {
      throw new CliError('No password on stdin.');
    }

    return secret;
  }

  if (ctx.mode !== 'interactive') {
    throw new CliError('Pass the password or access token on stdin with --password-stdin.');
  }

  const secret = await p.password({
    message: 'Password or access token',
    validate: value => (value ? undefined : 'Enter the password or access token.'),
  });

  if (p.isCancel(secret)) {
    ctx.log('Cancelled.');
    process.exit(0);
  }

  return secret;
}

/** Registry keys belong to the organization; only owners and admins may change them. */
export function explainForbidden(error: unknown): never {
  if (error instanceof ForbiddenError) {
    throw new CliError('Only organization owners and admins can change registry keys, with an API key that has Account write access.');
  }

  throw error;
}

export const registryKeysAddCommand = new CliCommand('add')
  .argument('<name>', 'name to pass with --registry')
  .description('Add a registry login for pulling private images')
  .requiredOption('--server <host>', 'registry host, e.g. docker.io or ghcr.io')
  .requiredOption('--username <user>', 'registry username')
  .option('--password-stdin', 'read the password or access token from stdin')
  .option('--profile <name>', 'profile to use', { complete: profiles })
  .addHelpText('after', '\nExamples:\n  featherless-cloud registry-keys add ghcr --server ghcr.io --username octocat\n  echo "$GHCR_TOKEN" | featherless-cloud registry-keys add ghcr --server ghcr.io --username octocat --password-stdin\n\nUse it with: featherless-cloud exec --image ghcr.io/octocat/app:1 --registry ghcr -- …')
  .action(async (name: string, options: { server: string; username: string; passwordStdin?: boolean; profile?: string }, cmd: Command) => {
    const ctx = createContext(cmd);
    const auth = await resolveAuth(options.profile);
    const secret = await readSecret(ctx, Boolean(options.passwordStdin));
    const client = new CloudClient(auth.apiBase, auth.apiKey);

    const key = await ctx.spin('Adding registry key', () => client.addRegistryCredential({
      name,
      registry: options.server,
      username: options.username,
      secret,
    })).catch(explainForbidden);

    ctx.success(`Added registry key ${key.name} for ${key.registry}. Use it with --registry ${key.name}.`);
    ctx.output(key, (item: RegistryCredential) => ({ text: item.id }));
  });
