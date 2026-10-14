import type { Command } from 'commander';
import type { CliCommand } from '../../completions';
import { createContext } from '../../context';
import { resolveAuth, consoleBaseUrl } from '../../config';
import { CloudClient, type TerminalSession } from '../../clients/cloud';
import { instances, resolveInstanceId, profiles } from '../shared';

/** Close-code copy mirrored from the console's InstanceTerminal component. */
const CLOSE_MESSAGES: Record<number, string> = {
  4001: 'Terminal session expired.',
  4002: 'You do not have access to this terminal.',
  4003: 'Terminal unavailable. Make sure the instance is running.',
  4004: 'Terminal could not start.',
  4005: 'Terminal closed after being idle.',
  4006: 'Terminal connection lost.',
  4007: 'Tmux session unavailable. It may have ended. Run the command again to open a new one.',
};

const RETRYABLE_CODES = new Set([4006]);
const RETRY_DELAYS_MS = [500, 1_000, 2_000];
interface ControlFrame {
  type?: string;
  status?: unknown;
  message?: string;
}


/**
 * The exit frame carries a status object, not a number.
 * Success means 0; a failed command reports its code in details.causes.
 */
function exitCodeOf(status: unknown): number {
  if (typeof status === 'number') {
    return status;
  }

  if (!status || typeof status !== 'object') {
    return 0;
  }

  const record = status as Record<string, unknown>;

  if (record.status === 'Success') {
    return 0;
  }

  const causes = (record.details as Record<string, unknown> | undefined)?.causes;

  if (Array.isArray(causes)) {
    const exitCause = causes.find(cause => (cause as Record<string, unknown>).reason === 'ExitCode');
    const code = Number((exitCause as Record<string, unknown> | undefined)?.message);

    if (Number.isInteger(code)) {
      return code;
    }
  }

  return 1;
}

interface SessionResult {
  /** Shell exit status, or null when the socket closed without one. */
  exitStatus: number | null;
  closeCode: number;
  serverError: string | null;
  reachedReady: boolean;
}

function clamp(value: number | undefined, fallback: number): number {
  const size = Math.floor(value ?? fallback);

  if (!Number.isFinite(size) || size < 1) {
    return fallback;
  }

  return Math.min(size, 1000);
}

const sleep = (ms: number): Promise<void> => {
  const { promise, resolve } = Promise.withResolvers<void>();
  setTimeout(resolve, ms);

  return promise;
};

/** Opens one terminal session; resolves when the shell exits or the socket closes. */
function runSession(apiBase: string, session: TerminalSession, columns: number, rows: number): Promise<SessionResult> {
  const { promise, resolve } = Promise.withResolvers<SessionResult>();
  const wsBase = apiBase.replace(/^http/, 'ws');

  const query = new URLSearchParams({
    token: session.token,
    cols: String(columns),
    rows: String(rows),
  });

  const ws = new WebSocket(`${wsBase}${session.wsPath}?${query.toString()}`);
  ws.binaryType = 'arraybuffer';

  let reachedReady = false;
  let serverError: string | null = null;
  let settled = false;

  const finish = (result: SessionResult): void => {
    if (settled) {
      return;
    }

    settled = true;
    detachStdio();
    resolve(result);
  };

  ws.onmessage = (event: MessageEvent) => {
    if (event.data instanceof ArrayBuffer) {
      process.stdout.write(Buffer.from(event.data));

      return;
    }

    let frame: ControlFrame;

    try {
      frame = JSON.parse(String(event.data)) as ControlFrame;
    } catch {
      return;
    }

    if (frame.type === 'ready') {
      reachedReady = true;
    } else if (frame.type === 'exit') {
      finish({ exitStatus: exitCodeOf(frame.status), closeCode: 1000, serverError: null, reachedReady });
      ws.close();
    } else if (frame.type === 'error') {
      serverError = frame.message || 'Terminal could not start.';
    }
  };

  ws.onclose = (event) => {
    finish({ exitStatus: null, closeCode: event.code, serverError, reachedReady });
  };

  ws.onerror = () => {
    // onclose always follows; keep the close-code path as the outcome.
  };

  attachStdio(ws);

  return promise;
}

let attachedSocket: WebSocket | null = null;

function onStdinData(chunk: Buffer): void {
  if (attachedSocket?.readyState === WebSocket.OPEN) {
    attachedSocket.send(chunk);
  }
}

function onStdoutResize(): void {
  if (attachedSocket?.readyState === WebSocket.OPEN && process.stdout.columns && process.stdout.rows) {
    attachedSocket.send(JSON.stringify({
      type: 'resize',
      columns: process.stdout.columns,
      rows: process.stdout.rows,
    }));
  }
}

function attachStdio(ws: WebSocket): void {
  attachedSocket = ws;
  process.stdin.setRawMode(true);
  process.stdin.resume();
  process.stdin.on('data', onStdinData);
  process.stdout.on('resize', onStdoutResize);
}

function detachStdio(): void {
  attachedSocket = null;
  process.stdin.removeListener('data', onStdinData);
  process.stdout.removeListener('resize', onStdoutResize);

  if (process.stdin.isTTY) {
    process.stdin.setRawMode(false);
  }

  process.stdin.pause();
}

export function registerInstanceShellCommand(parent: CliCommand): void {
  parent
    .command('shell')
    .argument('<id>', 'instance name or short id', { complete: instances })
    .description('Open an interactive shell in the instance (the shared tmux session "default")')
    .option('--profile <name>', 'profile to use', { complete: profiles })
    .action(async (rawId: string, options: { profile?: string }, cmd: Command) => {
      const ctx = createContext(cmd);

      if (ctx.mode === 'json' || !process.stdin.isTTY || !process.stdout.isTTY) {
        ctx.fatal('instance shell needs an interactive terminal.');
      }

      const auth = await resolveAuth(options.profile);
      const client = new CloudClient(auth.apiBase, auth.apiKey);
      const id = await resolveInstanceId(client, rawId);

      const instance = await client.getInstance(id);

      if (instance.state !== 'running') {
        ctx.fatal(`Instance ${instance.name} is ${instance.state}. Start it first: featherless-cloud instance start ${instance.shortId}`);
      }

      const keys = await client.sshKeys().catch(() => []);

      if (keys.length === 0) {
        ctx.warn(`No SSH keys registered — web terminal only. Add keys at ${consoleBaseUrl()}/credentials/ssh-keys.`);
      }

      const columns = clamp(process.stdout.columns, 80);
      const rows = clamp(process.stdout.rows, 24);

      let retry = 0;
      let hadReadyChain = false;
      let tmuxSessionId: string | undefined;

      for (;;) {
        // A fresh grant on reconnect reattaches the exact session it attached
        // to; a vanished tmux session is never recreated.
        const session = await client.createTerminalSession(
          id,
          columns,
          rows,
          tmuxSessionId ? { tmuxSessionId } : { tmux: 'default' },
        );

        tmuxSessionId = session.tmuxSession?.id ?? tmuxSessionId;
        // Tokens are single-use with a 15s TTL: connect immediately after mint.
        const result = await runSession(auth.apiBase, session, columns, rows);

        if (result.exitStatus !== null) {
          process.exit(result.exitStatus);
        }

        if (result.reachedReady) {
          hadReadyChain = true;
        }

        const retryable = RETRYABLE_CODES.has(result.closeCode) && hadReadyChain && retry < RETRY_DELAYS_MS.length;

        if (retryable) {
          ctx.log(CLOSE_MESSAGES[result.closeCode] ?? 'Terminal connection lost.');
          await sleep(RETRY_DELAYS_MS[retry] ?? 2_000);
          retry += 1;
          continue;
        }

        // The server's 4007 text points at the browser's session list.
        const message = (result.closeCode === 4007 ? CLOSE_MESSAGES[4007] : result.serverError)
          ?? CLOSE_MESSAGES[result.closeCode]
          ?? 'Terminal closed.';

        if (result.closeCode === 1000) {
          detachStdio();
          process.exit(0);
        }

        ctx.error(message);
        detachStdio();
        process.exit(1);
      }
    });
}
