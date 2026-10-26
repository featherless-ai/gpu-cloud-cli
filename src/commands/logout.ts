import type { Command } from 'commander';
import { CliCommand } from '../completions';
import { profiles } from './shared';
import { createContext } from '../context';
import { CliError } from '../error';
import { consoleBaseUrl, loadConfig, saveConfig } from '../config';

export const logoutCommand = new CliCommand('logout')
  .description('Remove stored credentials for a profile')
  .option('--profile <name>', 'profile to remove (default: current)', { complete: profiles })
  .action(async (options: { profile?: string }, cmd: Command) => {
    const ctx = createContext(cmd);

    const config = await loadConfig();
    const name = options.profile ?? config.currentProfile;

    if (!name || !config.profiles[name]) {
      throw new CliError('Not logged in.');
    }

    delete config.profiles[name];

    if (config.currentProfile === name) {
      config.currentProfile = '';
    }

    await saveConfig(config);

    ctx.success(
      `Signed out of profile "${name}". Note: revoke the key at ${consoleBaseUrl()}/credentials/api-keys to fully disconnect this device.`,
    );
  });
