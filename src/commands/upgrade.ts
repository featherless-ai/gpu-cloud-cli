import path from 'node:path';
import fs from 'node:fs/promises';
import type { Command } from 'commander';
import type { CliCommand } from '../completions';
import { createContext } from '../context';
import { CliError } from '../error';
import { IS_BINARY, VERSION, isNewer } from '../version';
import { RELEASES_BASE_URL, fetchLatestRelease } from '../update-check';

export function platformKey(): string {
  return `${process.platform}-${process.arch}`;
}

interface UpgradeResult {
  package: string;
  current: string;
  latest: string;
  upgraded: boolean;
}

export function registerUpgradeCommand(program: CliCommand): void {
  program
    .command('upgrade')
    .description('Upgrade featherless-cloud to the latest published build')
    .option('--force', 'reinstall even when already up to date')
    .action(async (options: { force?: boolean }, cmd: Command) => {
      const ctx = createContext(cmd);

      if (!IS_BINARY) {
        throw new CliError('This build was not compiled as a standalone binary. Run: bun run build');
      }

      if (RELEASES_BASE_URL === '') {
        throw new CliError('Automatic updates are not configured yet. Rebuild from source: pnpm --filter @featherless-cloud/cli build');
      }

      const current = VERSION;
      const latest = await ctx.spin('Checking for updates', () => fetchLatestRelease());

      if (!options.force && !isNewer(latest.version, current)) {
        const result: UpgradeResult = { package: 'featherless-cloud', current, latest: latest.version, upgraded: false };
        ctx.output(result, () => ({ text: `Already up to date (${current}).` }));
        return;
      }

      const key = platformKey();
      const asset = latest.assets[key];

      if (!asset) {
        throw new CliError(`No build published for ${key}. Supported: ${Object.keys(latest.assets).join(', ')}`);
      }

      const target = process.execPath;
      const staging = path.join(path.dirname(target), `${path.basename(target)}.new`);

      await ctx.spin(`Downloading ${latest.version}`, async () => {
        const response = await fetch(`${RELEASES_BASE_URL}/${latest.version}/${asset}`, { redirect: 'follow' });

        if (!response.ok) {
          throw new CliError(`Download failed with status ${response.status}.`);
        }

        const bytes = Buffer.from(await response.arrayBuffer());
        await fs.writeFile(staging, bytes, { mode: 0o755 });
        await fs.chmod(staging, 0o755);
      });

      try {
        await fs.rename(staging, target);
      } catch (err) {
        await fs.rm(staging, { force: true });
        throw new CliError(`Could not replace the running binary: ${err instanceof Error ? err.message : String(err)}`);
      }

      const result: UpgradeResult = { package: 'featherless-cloud', current, latest: latest.version, upgraded: true };
      ctx.output(result, () => ({ text: `Upgraded ${current} → ${latest.version}.` }));
    });
}
