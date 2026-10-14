import { EventEmitter } from 'node:events';
import { spawn } from 'node:child_process';
import {
  afterEach, beforeEach, describe, expect, it, vi,
} from 'vitest';
import { sshArgs, runSsh, tmuxCommand, validateSessionName } from '../src/commands/instance/ssh';
import { registerInstanceCommand } from '../src/commands/instance';
import { CliCommand, commandTree } from '../src/completions';
import { resolveAuth } from '../src/config';
import { CloudClient } from '../src/clients/cloud';
import type { InstanceDto } from '../src/types/instance';
import { CliContext } from '../src/context';

vi.mock('node:child_process', () => ({ spawn: vi.fn() }));
vi.mock('../src/config', () => ({ resolveAuth: vi.fn(), consoleBaseUrl: () => 'https://cloud.example' }));
vi.mock('../src/clients/cloud', () => ({ CloudClient: vi.fn() }));

const instance = {
  shortId: 'rcsrvch012',
  name: 'research',
  state: 'running',
  ssh: { host: 'ssh.example.com', hasKeys: true, command: 'DO NOT EXECUTE; touch /tmp/bad', status: 'ready' },
} as InstanceDto;

function program(): CliCommand {
  const root = new CliCommand('featherless-cloud').option('--json');
  registerInstanceCommand(root);
  return root;
}

describe('native SSH argv', () => {
  it('uses only structured connection data and keeps identity paths as one argument', () => {
    expect(sshArgs(instance)).toEqual(['rcsrvch012@ssh.example.com']);
    expect(sshArgs(instance, '/key dir/$(bad)')).toEqual(['-i', '/key dir/$(bad)', 'rcsrvch012@ssh.example.com']);
    expect(sshArgs(instance, undefined, 'main')).toEqual(['-t', 'rcsrvch012@ssh.example.com', tmuxCommand('main')]);
  });

  it('quotes the remote name and provides an actionable missing-tmux failure', () => {
    expect(tmuxCommand('train-1')).toContain('exec tmux new-session -A -s \'train-1\'');
    expect(tmuxCommand('main')).toContain('command -v tmux');
    expect(tmuxCommand('main')).toContain('Use an image with tmux');
    expect(tmuxCommand('main')).toContain('exit 127');
  });

  it.each(['', '-bad', '_run', 'a.b', 'a:b', '=main', 'foo*', 'a b', 'a\'b', 'a;exit', 'a\n', 'a'.repeat(65)])('rejects unsafe or ambiguous name %j', name => {
    expect(() => validateSessionName(name)).toThrow('Session name');
  });

  it.each(['main', 'train-1', 'run_1', '1', 'a'.repeat(64)])('accepts literal name %s', name => {
    expect(validateSessionName(name)).toBe(name);
  });

  it.each(['-oProxyCommand=bad', 'host;bad', 'host\n', 'user@host', 'host/path', ''])('rejects unsafe host %j', host => {
    expect(() => sshArgs({ ...instance, ssh: { ...instance.ssh!, host } })).toThrow('connection details');
  });

  it('accepts IPv6 and rejects malformed SSH users or absent details', () => {
    expect(sshArgs({ ...instance, ssh: { ...instance.ssh!, host: '::1' } })).toEqual(['rcsrvch012@::1']);
    expect(() => sshArgs({ ...instance, shortId: '-x@bad' })).toThrow('connection details');
    expect(() => sshArgs({ ...instance, ssh: undefined })).toThrow('connection details');
  });
});

describe('SSH process lifecycle', () => {
  let child: EventEmitter;

  beforeEach(() => {
    child = new EventEmitter();
    vi.mocked(spawn).mockReturnValue(child as ReturnType<typeof spawn>);
  });

  afterEach(() => {
    vi.clearAllMocks();
    vi.restoreAllMocks();
    process.exitCode = undefined;
  });

  it.each([0, 1, 127, 255])('inherits stdio without a local shell and propagates exit %i', async code => {
    const result = runSsh(['user@host']);
    expect(spawn).toHaveBeenCalledWith('ssh', ['user@host'], { shell: false, stdio: 'inherit' });
    child.emit('close', code, null);
    await expect(result).resolves.toBe(code);
  });

  it.each([['SIGTERM', 143], ['SIGINT', 130], ['SIGKILL', 137]])('propagates signal %s', async (signal, code) => {
    const result = runSsh([]);
    child.emit('close', null, signal);
    await expect(result).resolves.toBe(code);
  });

  it.each(['SIGINT', 'SIGTERM', 'SIGHUP'] as const)('forwards %s and removes the signal handler on exit', async signal => {
    const kill = vi.fn();
    Object.assign(child, { kill });
    const previous = process.listenerCount(signal);
    const result = runSsh([]);
    expect(process.listenerCount(signal)).toBe(previous + 1);
    process.emit(signal);
    expect(kill).toHaveBeenCalledWith(signal);
    child.emit('close', 0, null);
    await result;
    expect(process.listenerCount(signal)).toBe(previous);
  });

  it.each([['ENOENT', 'Install ssh'], ['EACCES', 'Could not start ssh']])('reports spawn error %s', async (code, message) => {
    const result = runSsh([]);
    child.emit('error', Object.assign(new Error('denied'), { code }));
    child.emit('close', -2, null);
    await expect(result).rejects.toThrow(message);
  });

  it.each([['ssh', undefined], ['tmux', 'default'], ['tmux', 'training']])('resolves instance/profile and runs %s %s', async (command, session) => {
    vi.mocked(resolveAuth).mockResolvedValue({ apiBase: 'https://api.example', apiKey: 'key', profile: 'work' });
    const getInstance = vi.fn().mockResolvedValue(instance);

    vi.mocked(CloudClient).mockImplementation(function () {
      return { getInstance } as unknown as CloudClient;
    });

    vi.mocked(spawn).mockImplementation(() => {
      queueMicrotask(() => child.emit('close', 23, null));
      return child as ReturnType<typeof spawn>;
    });

    const args = [command, instance.shortId, '-i', '/key dir/id', '--profile', 'work'];

    if (session === 'training') {
      args.push(session);
    }

    await program().parseAsync(['instance', ...args], { from: 'user' });
    expect(resolveAuth).toHaveBeenCalledWith('work');
    expect(getInstance).toHaveBeenCalledWith(instance.shortId);
    expect(spawn).toHaveBeenCalledWith('ssh', sshArgs(instance, '/key dir/id', session), { shell: false, stdio: 'inherit' });
    expect(process.exitCode).toBe(23);
  });

  it.each([
    { ...instance, state: 'stopped' },
    { ...instance, ssh: { ...instance.ssh!, hasKeys: false } },
    { ...instance, ssh: undefined },
  ])('refuses unavailable SSH without spawning a process', async unavailable => {
    vi.spyOn(CliContext.prototype, 'fatal').mockImplementation(message => {
      throw new Error(message);
    });

    vi.mocked(resolveAuth).mockResolvedValue({ apiBase: 'https://api.example', apiKey: 'key', profile: 'work' });

    vi.mocked(CloudClient).mockImplementation(function () {
      return { getInstance: vi.fn().mockResolvedValue(unavailable) } as unknown as CloudClient;
    });

    await expect(program().parseAsync(['instance', 'ssh', instance.shortId], { from: 'user' })).rejects.toThrow();
    expect(spawn).not.toHaveBeenCalled();
  });

  it('rejects JSON mode before authentication or spawning', async () => {
    vi.spyOn(CliContext.prototype, 'fatal').mockImplementation(message => {
      throw new Error(message);
    });

    await expect(program().parseAsync(['--json', 'instance', 'ssh', 'id'], { from: 'user' })).rejects.toThrow('do not support --json');
    expect(resolveAuth).not.toHaveBeenCalled();
    expect(spawn).not.toHaveBeenCalled();
  });

  it('validates names before making API calls', async () => {
    await expect(program().parseAsync(['instance', 'tmux', 'id', 'bad:name'], { from: 'user' })).rejects.toThrow('Session name');
    expect(resolveAuth).not.toHaveBeenCalled();
    expect(spawn).not.toHaveBeenCalled();
  });
});

it('registers shell, ssh and tmux with help and completion options', () => {
  const root = program();
  const group = root.commands.find(cmd => cmd.name() === 'instance')!;
  expect(group.commands.map(cmd => cmd.name())).toEqual(expect.arrayContaining(['shell', 'ssh', 'tmux']));
  const tmux = group.commands.find(cmd => cmd.name() === 'tmux')!;
  expect(tmux.helpInformation()).toContain('default: default');
  expect(tmux.helpInformation()).toContain('-i, --identity <file>');
  let help = '';

  tmux.configureOutput({
    writeOut: text => {
      help += text;
    },
  }).outputHelp();

  expect(help).toContain('default socket');
  expect(help).toContain('Ctrl-b d');
  expect(help).toContain('stop/restart/replacement');
  const node = commandTree(root).commands.find(cmd => cmd.name === 'instance')!;

  for (const name of ['ssh', 'tmux']) {
    expect(node.commands.find(cmd => cmd.name === name)?.options).toEqual(expect.arrayContaining([
      expect.objectContaining({ flags: '-i, --identity <file>', required: true }),
      expect.objectContaining({ flags: '--profile <name>', required: true }),
    ]));
  }
});
