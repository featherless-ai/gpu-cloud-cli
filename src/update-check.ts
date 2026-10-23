import path from 'node:path';
import { execFile, spawn } from 'node:child_process';
import { readFileSync, writeFileSync, mkdirSync, realpathSync } from 'node:fs';
import { promisify } from 'node:util';
import { getConfigDir } from './config';
import { PACKAGE_NAME, VERSION, isNewer } from './version';

const TTL_MS = 24 * 60 * 60 * 1000;

interface UpdateState {
  latest: string;
  checkedAt: number;
  /** The version the background check installed, announced once on the next run. */
  installed?: string;
}

function stateFile(): string {
  return path.join(getConfigDir(), 'featherless-cloud-update-check.json');
}

function readState(): UpdateState | null {
  try {
    return JSON.parse(readFileSync(stateFile(), 'utf8')) as UpdateState;
  } catch {
    return null;
  }
}

function writeState(state: UpdateState): void {
  try {
    mkdirSync(getConfigDir(), { recursive: true });
    writeFileSync(stateFile(), JSON.stringify(state) + '\n');
  } catch {
    // best-effort; a failed write just means we re-check next run
  }
}

function shouldSkip(): boolean {
  if (process.env.CI) {
    return true;
  }

  if (!process.stderr.isTTY) {
    return true;
  }

  const argv = process.argv.slice(2);
  return argv.includes('--json') || argv.includes('--non-interactive') || argv.includes('_check-update');
}

export async function fetchLatestVersion(): Promise<string> {
  const response = await fetch(`https://registry.npmjs.org/${PACKAGE_NAME}/latest`, {
    signal: AbortSignal.timeout(5000),
  });

  if (!response.ok) {
    throw new Error(`Release lookup failed with status ${response.status}.`);
  }

  const parsed = await response.json() as { version?: unknown };

  if (typeof parsed.version !== 'string') {
    throw new Error('Release index is malformed.');
  }

  return parsed.version;
}

function scheduleBackgroundCheck(prev: UpdateState | null): void {
  // Stamp checkedAt now (preserving any known latest) so rapid successive
  // runs don't each spawn a child before the first one writes its result.
  writeState({ latest: prev?.latest ?? VERSION, checkedAt: Date.now() });

  try {
    const script = process.argv[1];

    if (!script) {
      return;
    }

    const child = spawn(process.execPath, [script, '_check-update'], {
      detached: true,
      stdio: 'ignore',
    });

    child.unref();
  } catch {
    // ignore; nothing we can do without blocking
  }
}

/**
 * Synchronously surface a cached "update available" banner (never blocks on the
 * network) and, if the cache is stale, fire off a detached child to refresh it
 * for next time. Call once at startup.
 */
export function maybeNotifyUpdate(): void {
  if (shouldSkip()) {
    return;
  }

  const state = readState();

  if (state?.installed === VERSION) {
    process.stderr.write(`\nUpdated featherless-cloud to ${VERSION}.\n\n`);
    writeState({ latest: state.latest, checkedAt: state.checkedAt });
  } else if (state && isNewer(state.latest, VERSION)) {
    process.stderr.write(
      `\nUpdate available: ${VERSION} → ${state.latest}. Run \`featherless-cloud upgrade\`.\n\n`,
    );
  }

  if (!state || Date.now() - state.checkedAt > TTL_MS) {
    scheduleBackgroundCheck(state);
  }
}

/** Whether the running script is this package inside npm's global folder, the only install npm can update in place. */
export function isGlobalNpmInstall(script: string, globalRoot: string): boolean {
  const packageDir = path.join(globalRoot, ...PACKAGE_NAME.split('/')) + path.sep;
  return script.startsWith(packageDir);
}

/** npm's global folders: under an explicit prefix, or next to this Node.js, as npm installs by default. */
function globalRoots(): string[] {
  const prefixes = [process.env.npm_config_prefix, path.dirname(path.dirname(process.execPath))];
  const roots: string[] = [];

  for (const prefix of prefixes) {
    if (!prefix) {
      continue;
    }

    const root = process.platform === 'win32' ? path.join(prefix, 'node_modules') : path.join(prefix, 'lib', 'node_modules');

    try {
      roots.push(realpathSync(root));
    } catch {
      // not an npm prefix
    }
  }

  return roots;
}

async function installLatest(version: string): Promise<boolean> {
  if (process.env.FEATHERLESS_CLOUD_DISABLE_AUTOUPDATE) {
    return false;
  }

  try {
    const script = realpathSync(process.argv[1] ?? '');

    if (!globalRoots().some(root => isGlobalNpmInstall(script, root))) {
      return false;
    }

    await promisify(execFile)('npm', ['install', '--global', `${PACKAGE_NAME}@${version}`], {
      shell: process.platform === 'win32',
      timeout: 300_000,
    });

    return true;
  } catch {
    return false;
  }
}

/**
 * Runs inside the detached child (`_check-update`): refresh the cache and, for a global npm install,
 * install the newer version. A failed install leaves the "Update available" notice to the next run.
 */
export async function runUpdateCheck(): Promise<void> {
  try {
    const latest = await fetchLatestVersion();
    writeState({ latest, checkedAt: Date.now() });

    if (isNewer(latest, VERSION) && await installLatest(latest)) {
      writeState({ latest, checkedAt: Date.now(), installed: latest });
    }
  } catch {
    // swallow — retry on the next stale window
  }
}
