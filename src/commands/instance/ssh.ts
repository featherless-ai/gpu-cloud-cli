import { spawn } from 'node:child_process';
import { constants } from 'node:os';
import { isIP } from 'node:net';
import type { Command } from 'commander';
import type { CliCommand } from '../../completions';
import { createContext } from '../../context';
import { resolveAuth } from '../../config';
import { CloudClient } from '../../clients/cloud';
import { instances, resolveInstanceId, profiles } from '../shared';
import type { InstanceDto } from '../../types/instance';

/** Restrict tmux names to literal, portable names (no target or pattern syntax). */
export function validateSessionName(name: string): string {
  if (!/^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$/.test(name)) {
    throw new Error('Session name must be 1–64 letters, digits, underscores or hyphens, starting with a letter or digit.');
  }

  return name;
}

/** OpenSSH sends a single command string to the remote login shell. */
function quote(value: string): string {
  return `'${value.replaceAll('\'', '\'\\\'\'')}'`;
}

/** Use -s, not a target selector: new-session -A matches the exact session name. */
export function tmuxCommand(name: string): string {
  validateSessionName(name);
  const missing = 'tmux is not installed in this instance. Use an image with tmux, or install it yourself via instance ssh, then retry.';

  return `command -v tmux >/dev/null 2>&1 || { printf '%s\\n' ${quote(missing)} >&2; exit 127; }; exec tmux new-session -A -s ${quote(name)}`;
}

/** Treat API connection fields as data; never execute the display-only ssh.command. */
export function sshArgs(instance: InstanceDto, identity?: string, session?: string): string[] {
  const host = instance.ssh?.host;

  if (!host || !(isIP(host) || /^(?=.{1,253}$)[A-Za-z0-9](?:[A-Za-z0-9.-]*[A-Za-z0-9])?$/.test(host))
    || !/^[A-Za-z0-9][A-Za-z0-9_-]*$/.test(instance.shortId)) {
    throw new Error('Instance SSH connection details are unavailable or invalid. Refresh the instance and retry.');
  }

  const args: string[] = [];

  if (identity !== undefined) {
    args.push('-i', identity);
  }

  if (session !== undefined) {
    args.push('-t');
  }

  args.push(`${instance.shortId}@${host}`);

  if (session !== undefined) {
    args.push(tmuxCommand(session));
  }

  return args;
}

/** Preserve native SSH terminal handling, configuration, agent and host-key checks. */
export function runSsh(args: string[]): Promise<number> {
  return new Promise((resolve, reject) => {
    const child = spawn('ssh', args, { shell: false, stdio: 'inherit' });
    // Signals sent to the CLI itself must also reach its native SSH child.
    const signals = ['SIGINT', 'SIGTERM', 'SIGHUP'] as const;

    const handlers = signals.map(signal => {
      const handler = () => {
        child.kill(signal);
      };

      process.on(signal, handler);
      return { signal, handler };
    });

    const cleanup = () => {
      for (const { signal, handler } of handlers) {
        process.off(signal, handler);
      }
    };

    child.once('error', (error: NodeJS.ErrnoException) => {
      cleanup();

      reject(new Error(error.code === 'ENOENT'
        ? 'OpenSSH client not found. Install ssh on this computer and retry.'
        : `Could not start ssh: ${error.message}`));
    });

    child.once('close', (code, signal) => {
      cleanup();
      // Use conventional shell signal statuses without killing the CLI before cleanup.
      resolve(code ?? (signal ? 128 + (constants.signals[signal] ?? 1) : 1));
    });
  });
}

interface SshOptions {
  identity?: string;
  profile?: string;
}

/** Shared action keeps API authentication separate from native SSH authentication. */
async function connect(rawId: string, options: SshOptions, cmd: Command, session?: string): Promise<void> {
  const ctx = createContext(cmd);

  if (ctx.mode === 'json') {
    ctx.fatal('instance ssh and instance tmux do not support --json.');
  }

  if (session !== undefined) {
    validateSessionName(session);
  }

  const auth = await resolveAuth(options.profile);
  const client = new CloudClient(auth.apiBase, auth.apiKey);
  const id = await resolveInstanceId(client, rawId);
  const instance = await client.getInstance(id);

  if (instance.state !== 'running') {
    ctx.fatal(`Instance ${instance.name} is ${instance.state}. Start it first: featherless-cloud instance start ${instance.shortId}`);
  }

  if (instance.ssh?.hasKeys === false) {
    ctx.fatal('No SSH keys registered. Add your public key in the console under Credentials → SSH keys, then retry.');
  }

  process.exitCode = await runSsh(sshArgs(instance, options.identity, session));
}

export function registerInstanceSshCommands(parent: CliCommand): void {
  parent.command('ssh')
    .argument('<id>', 'instance name or short id', { complete: instances })
    .description('Open a shell using your local OpenSSH client')
    .option('-i, --identity <file>', 'SSH private key file (otherwise use SSH config/agent)')
    .option('--profile <name>', 'profile to use', { complete: profiles })
    .addHelpText('after', '\nUses native SSH config, agent and host-key checks. Register your public key in the console first.\nExit with exit, or Enter then ~. (the normal SSH escape).')
    .action((id: string, options: SshOptions, cmd: Command) => connect(id, options, cmd));

  parent.command('tmux')
    .argument('<id>', 'instance name or short id', { complete: instances })
    .argument('[name]', 'tmux session name')
    .description('Create or attach an exact named tmux session over SSH (default: default, the session the browser terminal opens)')
    .option('-i, --identity <file>', 'SSH private key file (otherwise use SSH config/agent)')
    .option('--profile <name>', 'profile to use', { complete: profiles })
    .addHelpText('after', '\nNames: 1–64 letters, digits, underscores or hyphens; start with a letter or digit.\nUses tmux on the default socket for the SSH login user; tmux must already be installed.\nDetach with Ctrl-b d; reconnect with the same name. Enter then ~. disconnects SSH.\nSessions survive disconnects, not instance stop/restart/replacement or tmux server exit.\nNo software is installed and no SSH keys are registered automatically.')
    .action((id: string, name: string | undefined, options: SshOptions, cmd: Command) => connect(id, options, cmd, name ?? 'default'));
}
