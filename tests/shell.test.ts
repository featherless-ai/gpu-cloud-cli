import {
  afterEach, beforeEach, expect, it, vi,
} from 'vitest';
import { instanceCommand } from '../src/commands/instance';
import { CliCommand } from '../src/completions';
import { resolveAuth } from '../src/config';
import { CloudClient } from '../src/clients/cloud';
import { CliContext } from '../src/context';

vi.mock('../src/config', () => ({ resolveAuth: vi.fn(), consoleBaseUrl: () => 'https://cloud.example' }));
vi.mock('../src/clients/cloud', () => ({ CloudClient: vi.fn() }));

type Frame = { type: string; message?: string };
type Script = { frames: Frame[]; closeCode: number };

let scripts: Script[] = [];

class FakeSocket {
  static readonly OPEN = 1;
  readyState = FakeSocket.OPEN;
  binaryType = '';
  onmessage: ((event: { data: string }) => void) | null = null;
  onclose: ((event: { code: number }) => void) | null = null;
  onerror: (() => void) | null = null;

  constructor() {
    const script = scripts.shift()!;

    queueMicrotask(() => {
      for (const frame of script.frames) {
        this.onmessage?.({ data: JSON.stringify(frame) });
      }

      this.onclose?.({ code: script.closeCode });
    });
  }

  send(): void {}
  close(): void {}
}

const restore: Array<() => void> = [];

function stub<T extends object>(target: T, key: string, value: unknown): void {
  const descriptor = Object.getOwnPropertyDescriptor(target, key);
  Object.defineProperty(target, key, { value, configurable: true, writable: true });
  restore.push(() => (descriptor ? Object.defineProperty(target, key, descriptor) : delete (target as Record<string, unknown>)[key]));
}

beforeEach(() => {
  stub(process.stdin, 'isTTY', true);
  stub(process.stdout, 'isTTY', true);
  stub(process.stdin, 'setRawMode', vi.fn());
  stub(process.stdin, 'resume', vi.fn());
  stub(process.stdin, 'pause', vi.fn());
  vi.stubGlobal('WebSocket', FakeSocket);
  vi.spyOn(CliContext.prototype, 'log').mockImplementation(() => {});

  vi.spyOn(process, 'exit').mockImplementation(code => {
    throw new Error(`exit ${code}`);
  });
});

afterEach(() => {
  restore.splice(0).reverse().forEach(undo => undo());
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

it('reattaches the tmux session it opened and explains a vanished one in CLI terms', async () => {
  const error = vi.spyOn(CliContext.prototype, 'error').mockImplementation(() => {});
  vi.mocked(resolveAuth).mockResolvedValue({ apiBase: 'https://api.example', apiKey: 'key', profile: 'work' });

  const createTerminalSession = vi.fn()
    .mockResolvedValueOnce({ token: 't1', wsPath: '/ws', tmuxSession: { id: 'tmux-1', name: 'default' } })
    .mockResolvedValueOnce({ token: 't2', wsPath: '/ws' });

  vi.mocked(CloudClient).mockImplementation(function () {
    return {
      getInstance: vi.fn().mockResolvedValue({ name: 'research', shortId: 'rcsrvch012', state: 'running' }),
      sshKeys: vi.fn().mockResolvedValue([{}]),
      createTerminalSession,
    } as unknown as CloudClient;
  });

  scripts = [
    { frames: [{ type: 'ready' }], closeCode: 4006 },
    { frames: [{ type: 'error', message: 'Tmux session could not be opened. Refresh the session list and try again.' }], closeCode: 4007 },
  ];

  const root = new CliCommand('featherless-cloud').option('--json').addCommand(instanceCommand);

  await expect(root.parseAsync(['instance', 'shell', 'rcsrvch012'], { from: 'user' })).rejects.toThrow('exit 1');
  expect(createTerminalSession.mock.calls.map(call => call[3])).toEqual([{ tmux: 'default' }, { tmuxSessionId: 'tmux-1' }]);
  expect(error).toHaveBeenCalledWith('Tmux session unavailable. It may have ended. Run the command again to open a new one.');
});
