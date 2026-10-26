import type { Command } from 'commander';
import { CliCommand } from '../completions';
import { profiles } from './shared';
import * as p from '@clack/prompts';
import { CliContext, createContext } from '../context';
import { CliError, ForbiddenError } from '../error';
import { apiBaseUrl, consoleBaseUrl, loadConfig, saveConfig } from '../config';
import { openBrowser } from '../auth/browser';
import { CloudClient } from '../clients/cloud';

// Mirrors API_KEY_SECRET_PATTERN in packages/api/src/lib/security/apiKeys.ts.
const KEY_PATTERN = /^flkey_[A-Za-z0-9_-]{43}$/;

/**
 * Confirms the secret exists. A 403 means the key is valid but lacks
 * `users:read`; the email is then unknown, which is acceptable.
 */
async function verifyKey(key: string): Promise<string | null> {
  try {
    const me = await new CloudClient(apiBaseUrl(), key).getMe();
    return me.data.user.email;
  } catch (err) {
    if (err instanceof ForbiddenError) {
      return null;
    }

    if (err instanceof CliError && err.message.startsWith('Could not connect')) {
      throw err;
    }

    throw new CliError('The API rejected that key. Check it was copied in full and has not been revoked.');
  }
}

async function storeProfile(
  ctx: CliContext,
  profile: string,
  key: string,
  email: string | null,
): Promise<void> {
  const config = await loadConfig();

  config.profiles[profile] = {
    apiBase: apiBaseUrl(),
    apiKey: key,
    createdAt: new Date().toISOString(),
    ...(email ? { email } : {}),
  };

  if (!config.currentProfile || Object.keys(config.profiles).length === 1) {
    config.currentProfile = profile;
  }

  await saveConfig(config);
  ctx.success(`Signed in${email ? ` as ${email}` : ''} (profile "${profile}").`);
}

export const loginCommand = new CliCommand('login')
  .description('Store an API key for this device')
  .option('--profile <name>', 'profile to store the credentials under', { defaultValue: 'default', complete: profiles })
  .option('--key <secret>', 'API key to store (non-interactive)')
  .action(async (options: { profile: string; key?: string }, cmd: Command) => {
    const ctx = createContext(cmd);
    const profile = options.profile;
    const key = options.key?.trim();

    if (key) {
      if (!KEY_PATTERN.test(key)) {
        throw new CliError('That does not look like an API key. Keys start with flkey_.');
      }

      const email = await ctx.spin('Verifying key', () => verifyKey(key));
      await storeProfile(ctx, profile, key, email);
      return;
    }

    if (ctx.mode !== 'interactive') {
      throw new CliError(
        `Pass the key with --key <secret>. Create one at ${consoleBaseUrl()}/credentials/api-keys.`,
      );
    }

    const url = `${consoleBaseUrl()}/credentials/api-keys?create=1`;
    ctx.log(`Create an API key in your browser, then paste it below.\n  ${url}`);
    openBrowser(url);

    for (;;) {
      const answer = await p.text({
        message: 'API key',
        placeholder: 'flkey_…',
        validate: value => {
          if (!value) {
            return 'Paste the key shown in the browser, or press Ctrl+C to cancel.';
          }

          if (!KEY_PATTERN.test(value)) {
            return 'That does not look like an API key. Keys start with flkey_.';
          }

          return undefined;
        },
      });

      if (p.isCancel(answer)) {
        p.cancel('Login cancelled.');
        process.exit(0);
      }

      try {
        const pasted = answer.trim();
        const email = await ctx.spin('Verifying key', () => verifyKey(pasted));
        await storeProfile(ctx, profile, pasted, email);
        return;
      } catch (err) {
        if (err instanceof CliError && err.message.startsWith('Could not connect')) {
          throw err;
        }

        p.log.warn('The API rejected that key. Create a new one in the browser and paste it again.');
      }
    }
  });
