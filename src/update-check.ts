import path from 'node:path';
import { spawn } from 'node:child_process';
import { readFileSync, writeFileSync, mkdirSync } from 'node:fs';
import { getConfigDir } from './config';
import { PACKAGE_NAME, VERSION, isNewer } from './version';

const TTL_MS = 24 * 60 * 60 * 1000;

interface UpdateState {
  latest: string;
  checkedAt: number;
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

  if (state && isNewer(state.latest, VERSION)) {
    process.stderr.write(
      `\nUpdate available: ${VERSION} → ${state.latest}. Run \`featherless-cloud upgrade\`.\n\n`,
    );
  }

  if (!state || Date.now() - state.checkedAt > TTL_MS) {
    scheduleBackgroundCheck(state);
  }
}

/** Runs inside the detached child (`_check-update`): refresh the cache. */
export async function runUpdateCheck(): Promise<void> {
  try {
    const latest = await fetchLatestVersion();
    writeState({ latest, checkedAt: Date.now() });
  } catch {
    // swallow — retry on the next stale window
  }
}
