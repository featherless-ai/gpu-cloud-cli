import {
  afterAll, afterEach, beforeAll, beforeEach, describe, expect, it,
} from 'vitest';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { isGlobalNpmInstall, offerUpdate } from '../src/update-check';
import { VERSION } from '../src/version';

let dir: string;
let stateFile: string;
let prevHome: string | undefined;
let prevCi: string | undefined;
let prevTty: boolean;
let stderr: string;
const realWrite = process.stderr.write.bind(process.stderr);

beforeAll(async () => {
  prevHome = process.env.HOME;
  dir = await fs.mkdtemp(path.join(os.tmpdir(), 'flcli-update-'));
  // The CLI derives its config dir from HOME, so isolating HOME isolates it.
  process.env.HOME = dir;
  stateFile = path.join(dir, '.config', 'featherless-cloud-update-check.json');

  process.stderr.write = ((chunk: string) => {
    stderr += chunk;
    return true;
  }) as typeof process.stderr.write;
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

beforeEach(async () => {
  stderr = '';
  prevCi = process.env.CI;
  prevTty = process.stderr.isTTY;
  delete process.env.CI;
  await fs.rm(path.join(dir, '.config'), { recursive: true, force: true });
});

afterEach(() => {
  process.stderr.isTTY = prevTty;

  if (prevCi !== undefined) {
    process.env.CI = prevCi;
  }
});

/** A state whose check is fresh, so no background check is started. */
async function knownLatest(latest: string, declined?: string): Promise<void> {
  await fs.mkdir(path.dirname(stateFile), { recursive: true });
  await fs.writeFile(stateFile, JSON.stringify({ latest, checkedAt: Date.now(), declined }));
}

describe('offerUpdate', () => {
  it('stays silent and writes nothing when stderr is not a terminal', async () => {
    process.stderr.isTTY = false;
    await offerUpdate(['instance', 'list']);

    expect(stderr).toBe('');
    await expect(fs.access(stateFile)).rejects.toThrow();
  });

  it('tells an install npm cannot update in place about a newer version', async () => {
    process.stderr.isTTY = true;
    await knownLatest('99.0.0');
    await offerUpdate(['instance', 'list']);

    expect(stderr).toBe(`\nUpdate available: ${VERSION} → 99.0.0. Run \`featherless-cloud upgrade\`.\n\n`);
  });

  it('does not offer a version the person declined', async () => {
    process.stderr.isTTY = true;
    await knownLatest('99.0.0', '99.0.0');
    await offerUpdate(['instance', 'list']);

    expect(stderr).toBe('');
  });

  it('offers a version newer than the declined one', async () => {
    process.stderr.isTTY = true;
    await knownLatest('99.1.0', '99.0.0');
    await offerUpdate(['instance', 'list']);

    expect(stderr).toContain('99.1.0');
  });

  it('never interrupts scripts, completion or upgrade itself', async () => {
    process.stderr.isTTY = true;
    await knownLatest('99.0.0');

    await offerUpdate(['instance', 'list', '--json']);
    await offerUpdate(['--non-interactive', 'volume', 'ls']);
    await offerUpdate(['_complete', 'instance']);
    await offerUpdate(['upgrade']);

    expect(stderr).toBe('');
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
