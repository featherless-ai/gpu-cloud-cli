import path from 'node:path';
import { spawn } from 'node:child_process';
import { readFileSync, writeFileSync, mkdirSync, realpathSync } from 'node:fs';
import * as p from '@clack/prompts';
import { getConfigDir } from './config';
import { CliError } from './error';
import { PACKAGE_NAME, VERSION, isNewer } from './version';

const TTL_MS = 24 * 60 * 60 * 1000;

// Commands that run without a person reading the output, or are about versions themselves.
const NO_PROMPT_COMMANDS = ['_check-update', '_complete', 'completion', 'upgrade'];

interface UpdateState {
  latest: string;
  checkedAt: number;
  /** A version the person said no to; they are asked again only about a newer one. */
  declined?: string;
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

function shouldSkip(argv: string[]): boolean {
  if (process.env.CI) {
    return true;
  }

  if (!process.stderr.isTTY) {
    return true;
  }

  const command = argv.find(arg => !arg.startsWith('-'));
  return argv.includes('--json') || argv.includes('--non-interactive') || NO_PROMPT_COMMANDS.includes(command ?? '');
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

/** Installs a version globally with npm. npm's own output goes to stderr in --json mode so stdout stays one JSON document. */
export function npmInstall(spec: string, json = false): Promise<void> {
  return new Promise((resolve, reject) => {
    const child = spawn('npm', ['install', '--global', '--prefer-online', spec], {
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

function installedGlobally(): boolean {
  try {
    const script = realpathSync(process.argv[1] ?? '');
    return globalRoots().some(root => isGlobalNpmInstall(script, root));
  } catch {
    return false;
  }
}

function scheduleBackgroundCheck(prev: UpdateState | null): void {
  // Stamp checkedAt now (preserving what is known) so rapid successive
  // runs don't each spawn a child before the first one writes its result.
  writeState({ ...prev, latest: prev?.latest ?? VERSION, checkedAt: Date.now() });

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
 * Offers a newer version the last background check found, then refreshes that check when it is
 * a day old. A global npm install at a terminal is asked whether to update now; anything else
 * gets a one-line notice. Never touches the network itself. Call once at startup.
 */
export async function offerUpdate(argv = process.argv.slice(2)): Promise<void> {
  if (shouldSkip(argv)) {
    return;
  }

  const state = readState();

  if (state && isNewer(state.latest, VERSION) && state.declined !== state.latest) {
    if (process.stdin.isTTY && installedGlobally()) {
      await askToUpdate(state);
    } else {
      process.stderr.write(`\nUpdate available: ${VERSION} → ${state.latest}. Run \`featherless-cloud upgrade\`.\n\n`);
    }
  }

  if (!state || Date.now() - state.checkedAt > TTL_MS) {
    scheduleBackgroundCheck(readState());
  }
}

async function askToUpdate(state: UpdateState): Promise<void> {
  const answer = await p.confirm({
    message: `featherless-cloud ${state.latest} is available (you have ${VERSION}). Update now?`,
  });

  if (p.isCancel(answer) || !answer) {
    writeState({ ...state, declined: state.latest });
    process.stderr.write('Run `featherless-cloud upgrade` whenever you want it.\n\n');
    return;
  }

  try {
    await npmInstall(`${PACKAGE_NAME}@${state.latest}`);
    process.stderr.write(`\nUpdated featherless-cloud to ${state.latest}.\n\n`);
  } catch (err) {
    process.stderr.write(`\n${err instanceof Error ? err.message : String(err)}\n\n`);
  }
}

/** Runs inside the detached child (`_check-update`): refresh the cache. */
export async function runUpdateCheck(): Promise<void> {
  try {
    const latest = await fetchLatestVersion();
    writeState({ ...readState(), latest, checkedAt: Date.now() });
  } catch {
    // swallow — retry on the next stale window
  }
}
