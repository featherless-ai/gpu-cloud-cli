import { access, readFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import type { Command } from 'commander';
import { CliCommand } from '../../completions';
import { createContext } from '../../context';
import { resolveAuth } from '../../config';
import { CloudClient } from '../../clients/cloud';
import { CliError } from '../../error';
import { profiles } from '../shared';
import type { SshKey } from '../../types/instance';

const DEFAULT_KEY_FILES = ['id_ed25519.pub', 'id_ecdsa.pub', 'id_rsa.pub'];

/** Validates an OpenSSH public key line and returns it with its comment. */
export function parsePublicKey(content: string): { publicKey: string; comment?: string } {
  const line = content.trim();

  if (line.includes('PRIVATE KEY')) {
    throw new CliError('That is a private key. Pass the matching .pub file instead.');
  }

  const [type, body, ...comment] = line.split(/\s+/);

  if (!type || !body || line.includes('\n') || !/^(ssh-|ecdsa-|sk-)/.test(type)) {
    throw new CliError('That is not an OpenSSH public key. Pass a .pub file, e.g. ~/.ssh/id_ed25519.pub.');
  }

  return { publicKey: line, comment: comment.join(' ') || undefined };
}

async function findDefaultKeyFile(): Promise<string> {
  const directory = path.join(os.homedir(), '.ssh');

  for (const name of DEFAULT_KEY_FILES) {
    const file = path.join(directory, name);
    const exists = await access(file).then(() => true, () => false);

    if (exists) {
      return file;
    }
  }

  throw new CliError('No public key found in ~/.ssh. Pass a .pub file, or create a key with ssh-keygen -t ed25519.');
}

export const sshKeysAddCommand = new CliCommand('add')
  .argument('[file]', 'public key file (default: ~/.ssh/id_ed25519.pub, id_ecdsa.pub or id_rsa.pub)')
  .description('Add an SSH public key for instance access')
  .option('--name <name>', 'key name (default: the key comment, or this computer\'s name)')
  .option('--profile <name>', 'profile to use', { complete: profiles })
  .action(async (file: string | undefined, options: { name?: string; profile?: string }, cmd: Command) => {
    const ctx = createContext(cmd);
    const keyFile = file ?? await findDefaultKeyFile();

    let content: string;

    try {
      content = await readFile(keyFile, 'utf8');
    } catch {
      throw new CliError(`Could not read ${keyFile}.`);
    }

    const { publicKey, comment } = parsePublicKey(content);
    const name = options.name?.trim() || comment || os.hostname();

    const auth = await resolveAuth(options.profile);
    const client = new CloudClient(auth.apiBase, auth.apiKey);

    const key = await ctx.spin('Adding SSH key', () => client.addSshKey({ name, publicKey }));

    ctx.success(`Added SSH key ${key.name} (${key.fingerprint}).`);
    ctx.output(key, (item: SshKey) => ({ text: item.id }));
  });
