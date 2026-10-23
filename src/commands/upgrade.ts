import { spawn } from 'node:child_process';
import type { Command } from 'commander';
import type { CliCommand } from '../completions';
import { createContext } from '../context';
import { CliError } from '../error';
import { PACKAGE_NAME, VERSION, isNewer } from '../version';
import { fetchLatestVersion } from '../update-check';

interface UpgradeResult {
  package: string;
  current: string;
  latest: string;
  upgraded: boolean;
}

// npm's own output goes to stderr in --json mode so stdout stays one JSON document.
function npmInstall(spec: string, json: boolean): Promise<void> {
  return new Promise((resolve, reject) => {
    const child = spawn('npm', ['install', '--global', spec], {
      stdio: ['ignore', json ? process.stderr : 'inherit', 'inherit'],
      shell: process.platform === 'win32',
    });

    child.on('error', (err) => {
      reject(new CliError(`Could not run npm: ${err.message}. Run: npm install --global ${spec}`));
    });

    child.on('exit', (code) => {
      if (code === 0) {
        resolve();
        return;
      }

      reject(new CliError(`npm exited with code ${code}. Run: npm install --global ${spec}`));
    });
  });
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
