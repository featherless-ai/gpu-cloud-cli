import {
  afterAll, beforeAll, describe, expect, it,
} from 'vitest';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { maybeNotifyUpdate } from '../src/update-check';

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
});
