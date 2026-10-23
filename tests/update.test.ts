import {
  afterAll, beforeAll, describe, expect, it,
} from 'vitest';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { isGlobalNpmInstall, maybeNotifyUpdate } from '../src/update-check';
import { VERSION } from '../src/version';

let dir: string;
let prevHome: string | undefined;
let banner: string;
let writeSpy: (chunk: string) => void;
const realWrite = process.stderr.write.bind(process.stderr);

beforeAll(async () => {
  prevHome = process.env.HOME;
  dir = await fs.mkdtemp(path.join(os.tmpdir(), 'flcli-update-'));
  // The CLI derives its config dir from HOME, so isolating HOME isolates it.
  process.env.HOME = dir;

  banner = '';

  writeSpy = (chunk: string) => {
    banner += chunk;
    return true;
  };

  process.stderr.write = writeSpy as typeof process.stderr.write;
});

afterAll(async () => {
  process.stderr.write = realWrite as typeof process.stderr.write;

  if (prevHome === undefined) {
    delete process.env.HOME;
  } else {
    process.env.HOME = prevHome;
  }

  await fs.rm(dir, { recursive: true, force: true });
});

describe('maybeNotifyUpdate', () => {
  it('writes no state and prints nothing when stderr is not a terminal', async () => {
    maybeNotifyUpdate();
    expect(banner).toBe('');
    const entries = await fs.readdir(path.join(dir, '.config')).catch(() => [] as string[]);
    expect(entries).not.toContain('featherless-cloud-update-check.json');
  });

  it('announces a version the background check installed, once', async () => {
    const stateFile = path.join(dir, '.config', 'featherless-cloud-update-check.json');
    await fs.mkdir(path.dirname(stateFile), { recursive: true });
    await fs.writeFile(stateFile, JSON.stringify({ latest: VERSION, checkedAt: Date.now(), installed: VERSION }));

    const tty = process.stderr.isTTY;
    const ci = process.env.CI;
    process.stderr.isTTY = true;
    delete process.env.CI;

    try {
      maybeNotifyUpdate();
      maybeNotifyUpdate();
    } finally {
      process.stderr.isTTY = tty;

      if (ci !== undefined) {
        process.env.CI = ci;
      }
    }

    expect(banner).toBe(`\nUpdated featherless-cloud to ${VERSION}.\n\n`);
  });
});

describe('isGlobalNpmInstall', () => {
  const root = path.join(path.sep, 'usr', 'local', 'lib', 'node_modules');

  it('accepts the package inside npm\'s global folder', () => {
    expect(isGlobalNpmInstall(path.join(root, '@featherlessai', 'gpu-cloud-cli', 'dist', 'index.js'), root)).toBe(true);
  });

  it('refuses npx caches, project installs and other packages', () => {
    const npx = path.join(path.sep, 'home', 'ada', '.npm', '_npx', 'abc', 'node_modules', '@featherlessai', 'gpu-cloud-cli', 'dist', 'index.js');
    const project = path.join(path.sep, 'work', 'app', 'node_modules', '@featherlessai', 'gpu-cloud-cli', 'dist', 'index.js');

    expect(isGlobalNpmInstall(npx, root)).toBe(false);
    expect(isGlobalNpmInstall(project, root)).toBe(false);
    expect(isGlobalNpmInstall(path.join(root, '@featherlessai', 'gpu-cloud-cli-extra', 'index.js'), root)).toBe(false);
  });
});
