import os from 'node:os';
import path from 'node:path';
import fs from 'node:fs/promises';
import YAML from 'yaml';
import { CliError } from './error';
import { IS_BINARY } from './version';

export interface Profile {
  apiBase: string;
  apiKey: string;
  email?: string;
  createdAt: string;
}

export interface CloudConfig {
  version: 1;
  currentProfile: string;
  profiles: Record<string, Profile>;
}

export interface ResolvedAuth {
  apiBase: string;
  apiKey: string;
  profile: string;
}

// The compiled binary targets the public cloud; running from source targets
// the local Docker Compose stack.
export function apiBaseUrl(): string {
  return IS_BINARY ? 'https://api.cloud.featherless.ai' : 'http://localhost:3001';
}

export function consoleBaseUrl(): string {
  return IS_BINARY ? 'https://console.cloud.featherless.ai' : 'http://localhost:4000';
}

// The config lives directly in the per-user config directory on every
// platform: ~/.config/featherless-cloud.yaml on macOS and Linux,
// %APPDATA%\featherless-cloud.yaml (Roaming) on Windows. APPDATA is read
// because the AppData path can be redirected; HOME is the homedir fallback.
export function getConfigDir(): string {
  if (process.platform === 'win32' && process.env.APPDATA) {
    return process.env.APPDATA;
  }

  return path.join(os.homedir(), '.config');
}

export function configFilePath(): string {
  return path.join(getConfigDir(), 'featherless-cloud.yaml');
}

function emptyConfig(): CloudConfig {
  return { version: 1, currentProfile: '', profiles: {} };
}

function isValidProfile(value: unknown): value is Profile {
  if (!value || typeof value !== 'object') {
    return false;
  }

  const profile = value as Record<string, unknown>;
  return typeof profile.apiBase === 'string' && typeof profile.apiKey === 'string';
}

export async function loadConfig(): Promise<CloudConfig> {
  const file = configFilePath();
  let raw: string;

  try {
    raw = await fs.readFile(file, 'utf8');
  } catch {
    return emptyConfig();
  }

  const invalid = new CliError(`Config file ${file} is not valid featherless-cloud config.`);

  let parsed: unknown;

  try {
    parsed = YAML.parse(raw);
  } catch {
    throw invalid;
  }

  if (!parsed || typeof parsed !== 'object') {
    throw invalid;
  }

  const config = parsed as Record<string, unknown>;

  if (config.version !== 1 || typeof config.currentProfile !== 'string') {
    throw invalid;
  }

  if (!config.profiles || typeof config.profiles !== 'object') {
    throw invalid;
  }

  const profiles = config.profiles as Record<string, unknown>;

  for (const entry of Object.values(profiles)) {
    if (!isValidProfile(entry)) {
      throw invalid;
    }
  }

  return {
    version: 1,
    currentProfile: config.currentProfile,
    profiles: profiles as Record<string, Profile>,
  };
}

export async function saveConfig(config: CloudConfig): Promise<void> {
  const dir = getConfigDir();
  await fs.mkdir(dir, { recursive: true });
  const file = configFilePath();
  await fs.writeFile(file, YAML.stringify(config), { mode: 0o600 });
  await fs.chmod(file, 0o600);
}

export async function resolveAuth(profileFlag?: string): Promise<ResolvedAuth> {
  if (profileFlag) {
    const config = await loadConfig();
    const profile = config.profiles[profileFlag];

    if (!profile) {
      throw new CliError(
        `No profile named "${profileFlag}". Run featherless-cloud login --profile ${profileFlag} first.`,
      );
    }

    return { apiBase: profile.apiBase, apiKey: profile.apiKey, profile: profileFlag };
  }

  const config = await loadConfig();

  if (!config.currentProfile) {
    throw new CliError('Not logged in. Run featherless-cloud login.');
  }

  const profile = config.profiles[config.currentProfile];

  if (!profile) {
    throw new CliError(`No profile named "${config.currentProfile}". Run featherless-cloud login first.`);
  }

  return { apiBase: profile.apiBase, apiKey: profile.apiKey, profile: config.currentProfile };
}
