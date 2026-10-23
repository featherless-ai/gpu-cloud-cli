import type { Command } from 'commander';
import type { CliCommand } from '../completions';
import { createContext } from '../context';
import { PACKAGE_NAME, VERSION, isNewer } from '../version';
import { fetchLatestVersion, npmInstall } from '../update-check';

interface UpgradeResult {
  package: string;
  current: string;
  latest: string;
  upgraded: boolean;
}

export function registerUpgradeCommand(program: CliCommand): void {
  program
    .command('upgrade')
    .description('Upgrade featherless-cloud to the latest published version')
    .option('--force', 'reinstall even when already up to date')
    .action(async (options: { force?: boolean }, cmd: Command) => {
      const ctx = createContext(cmd);
      const current = VERSION;
      const latest = await ctx.spin('Checking for updates', () => fetchLatestVersion());

      if (!options.force && !isNewer(latest, current)) {
        const result: UpgradeResult = { package: PACKAGE_NAME, current, latest, upgraded: false };
        ctx.output(result, () => ({ text: `Already up to date (${current}).` }));
        return;
      }

      await npmInstall(`${PACKAGE_NAME}@${latest}`, ctx.mode === 'json');

      const result: UpgradeResult = { package: PACKAGE_NAME, current, latest, upgraded: true };
      ctx.output(result, () => ({ text: `Upgraded ${current} → ${latest}.` }));
    });
}
