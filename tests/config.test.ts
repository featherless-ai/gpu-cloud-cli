import {
  afterAll, beforeAll, describe, expect, it,
} from 'vitest';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { configFilePath, loadConfig, saveConfig } from '../src/config';
import { CliError } from '../src/error';

let dir: string;
let prevHome: string | undefined;

beforeAll(async () => {
  prevHome = process.env.HOME;
  dir = await fs.mkdtemp(path.join(os.tmpdir(), 'flcli-config-'));
  // The CLI derives its config dir from HOME, so isolating HOME isolates it.
  process.env.HOME = dir;
});

afterAll(async () => {
  if (prevHome === undefined) {
    delete process.env.HOME;
  } else {
    process.env.HOME = prevHome;
  }

  await fs.rm(dir, { recursive: true, force: true });
});

describe('loadConfig', () => {
  it('returns the empty shape when the file is missing', async () => {
    expect(await loadConfig()).toEqual({ version: 1, currentProfile: '', profiles: {} });
  });

  it('round-trips through saveConfig', async () => {
    const config = {
      version: 1 as const,
      currentProfile: 'default',
      profiles: {
        default: {
          apiBase: 'https://api.cloud.featherless.ai',
          apiKey: 'flkey_test',
          email: 'dev@example.test',
          createdAt: new Date(0).toISOString(),
        },
      },
    };

    await saveConfig(config);
    expect(await loadConfig()).toEqual(config);
  });

  it('rejects malformed YAML with the exact message', async () => {
    await fs.mkdir(path.dirname(configFilePath()), { recursive: true });
    const file = configFilePath();
    await fs.writeFile(file, '\tinvalid: [unclosed');

    await expect(loadConfig()).rejects.toThrow(
      new CliError(`Config file ${file} is not valid featherless-cloud config.`),
    );
  });

  it('rejects entries missing apiBase or apiKey', async () => {
    await fs.mkdir(path.dirname(configFilePath()), { recursive: true });
    await fs.writeFile(configFilePath(), 'version: 1\ncurrentProfile: x\nprofiles:\n  x:\n    apiBase: https://a\n');
    await expect(loadConfig()).rejects.toThrow(CliError);
  });
});

describe('saveConfig', () => {
  it('stores credentials with mode 0600', async () => {
    await saveConfig({ version: 1, currentProfile: '', profiles: {} });
    const stat = await fs.stat(configFilePath());
    expect(stat.mode & 0o777).toBe(0o600);
  });
});
