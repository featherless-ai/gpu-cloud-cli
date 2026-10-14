import { createReadStream, createWriteStream } from 'node:fs';
import fs from 'node:fs/promises';
import path from 'node:path';
import { Readable, Transform } from 'node:stream';
import { pipeline } from 'node:stream/promises';
import { z } from 'zod';
import { getConfigDir } from '../config';
import { CliError } from '../error';
import { VERSION } from '../version';
import type { CloudClient, FileSession } from './cloud';

export const fileEntrySchema = z.object({
  name: z.string(),
  path: z.string(),
  type: z.enum(['file', 'directory']),
  sizeBytes: z.number().optional(),
  modifiedAt: z.string(),
  version: z.string().optional(),
});

const listingSchema = z.object({
  path: z.string(),
  entries: z.array(fileEntrySchema),
  nextCursor: z.string().optional(),
});

export type FileEntry = z.infer<typeof fileEntrySchema>;
export type UploadMode = 'create' | 'overwrite';

/** How a find page ended: done, or stopped at a bound with a cursor that continues the walk. */
export type FindEnd = { done: true } | { done: false; cursor?: string };

/** What `find` narrows to; sizes and times are exclusive bounds, and a size bound leaves out folders. */
export interface FindFilters {
  name?: string;
  ignoreCase?: boolean;
  type?: 'file' | 'directory';
  maxDepth?: number;
  minSize?: number;
  maxSize?: number;
  modifiedAfter?: Date;
  modifiedBefore?: Date;
}

/** A refusal from the file API, with its machine code. */
export class FileApiError extends CliError {
  constructor(message: string, readonly status: number, readonly code?: string) {
    super(message);
    this.name = 'FileApiError';
  }
}

/** Ask for a new token this long before the current one expires. */
const REFRESH_MARGIN_MS = 60_000;
const RETRY_DELAYS_MS = [1_000, 2_000, 5_000, 10_000, 20_000, 30_000];
const RETRYABLE = new Set([423, 429, 500, 502, 503, 504]);
/** Files up to this size go in the request that creates the upload. */
const INLINE_UPLOAD_BYTES = 4 * 1024 * 1024;
/** Each PATCH body; Cloudflare refuses request bodies over 100 MB. */
const UPLOAD_CHUNK_BYTES = 64 * 1024 * 1024;
/** A larger file goes as up to this many lanes at once, partial uploads the server joins. */
const LANES = 4;
const LANE_PIECE_BYTES = 16 * 1024 * 1024;
/** An upload request whose bytes stop moving this long is given up and resumed, as after a dropped connection. */
const STALL_MS = 30_000;
const UPLOAD_STATE_TTL_MS = 24 * 60 * 60_000;

export interface FilesClientOptions {
  /** Test seam: how long to wait before retry number `attempt`. */
  delay?: (attempt: number, retryAfterSeconds: number | null) => number;
  /** Test seam: where unfinished uploads are remembered between runs. */
  stateFile?: string;
  /** Test seam: the most one PATCH carries. */
  chunkBytes?: number;
  /** Test seam: how long an upload request may make no progress. */
  stallMs?: number;
}

const sleep = (ms: number) => new Promise(resolve => setTimeout(resolve, ms));

function encodeMetadata(metadata: Record<string, string | undefined>): string {
  return Object.entries(metadata)
    .filter(([, value]) => value !== undefined)
    .map(([key, value]) => `${key} ${Buffer.from(value as string).toString('base64')}`)
    .join(',');
}

async function refusal(response: Response): Promise<FileApiError> {
  const text = await response.text().catch(() => '');
  let parsed: { error?: { code?: string; message?: string } } | undefined;

  try {
    parsed = JSON.parse(text);
  } catch {
    parsed = undefined;
  }

  return new FileApiError(parsed?.error?.message ?? `File request failed (${response.status}).`, response.status, parsed?.error?.code);
}

/** Passes bytes through, reporting how many have gone by. */
function counter(onBytes: (count: number) => void): Transform {
  let count = 0;

  return new Transform({
    transform(chunk: Buffer, _encoding, done) {
      count += chunk.length;
      onBytes(count);
      done(null, chunk);
    },
  });
}

/**
 * The volume file API, at the host the cloud API names. Tokens come from the
 * cloud API and are renewed before they expire. Uploads and downloads resume
 * after a dropped connection, and an upload interrupted in an earlier run
 * resumes in the next one.
 */
export class FilesClient {
  private session: FileSession | null = null;
  private pending: Promise<FileSession> | null = null;
  private readonly delay: (attempt: number, retryAfterSeconds: number | null) => number;
  private readonly stateFile: string;
  private readonly chunkBytes: number;
  private readonly stallMs: number;

  constructor(private readonly cloud: Pick<CloudClient, 'createFileSession'>, readonly volumeId: string, options: FilesClientOptions = {}) {
    this.delay = options.delay ?? ((attempt, retryAfter) => retryAfter !== null ? Math.min(retryAfter, 60) * 1_000 : RETRY_DELAYS_MS[attempt] ?? 30_000);
    this.stateFile = options.stateFile ?? path.join(getConfigDir(), 'featherless-cloud-uploads.json');
    this.chunkBytes = options.chunkBytes ?? UPLOAD_CHUNK_BYTES;
    this.stallMs = options.stallMs ?? STALL_MS;
  }

  private current(refresh = false): Promise<FileSession> {
    if (!refresh && this.session && Date.parse(this.session.expiresAt) - Date.now() > REFRESH_MARGIN_MS) {
      return Promise.resolve(this.session);
    }

    this.pending ??= this.cloud.createFileSession(this.volumeId)
      .then(created => this.session = created)
      .finally(() => this.pending = null);

    return this.pending;
  }

  /** One request with a current token; `target` is a path on the file host or an absolute URL. */
  private async once(target: string, init: RequestInit, refresh = false): Promise<Response> {
    const session = await this.current(refresh);
    const headers = new Headers(init.headers);
    headers.set('Authorization', `Bearer ${session.token}`);
    headers.set('User-Agent', `featherless-cloud/${VERSION}`);
    headers.set('Tus-Resumable', '1.0.0');
    const url = target.startsWith('http') ? target : `${session.baseUrl}${target}`;
    const response = await fetch(url, { ...init, headers });

    if (response.status === 401 && !refresh) {
      const body = await response.clone().text().catch(() => '');

      if (body.includes('token_expired')) {
        await response.body?.cancel();
        return this.once(target, init, true);
      }
    }

    // The file server didn't start in time. A new session makes sure it exists; the caller's
    // retry then goes out with that session.
    if (response.status === 503 && (await response.clone().text().catch(() => '')).includes('files_starting')) {
      await this.current(true);
    }

    return response;
  }

  /** A replayable request, retried on dropped connections and busy answers. */
  private async send(target: string, init: RequestInit = {}): Promise<Response> {
    for (let attempt = 0; ; attempt += 1) {
      let response: Response | null = null;

      try {
        response = await this.once(target, init);
      } catch (error) {
        if (attempt >= RETRY_DELAYS_MS.length) {
          throw new CliError(`Could not reach the file service: ${(error as Error).message}`);
        }
      }

      if (response?.ok) {
        return response;
      }

      if (response && (!RETRYABLE.has(response.status) || attempt >= RETRY_DELAYS_MS.length)) {
        throw await refusal(response);
      }

      const retryAfter = response?.headers.get('retry-after');
      await response?.body?.cancel();
      await sleep(this.delay(attempt, retryAfter ? Number(retryAfter) : null));
    }
  }

  private route(suffix: string, query: Record<string, string | undefined> = {}): string {
    const params = new URLSearchParams(Object.entries(query).filter((entry): entry is [string, string] => entry[1] !== undefined));
    const search = params.toString();
    return `/v1/volumes/${this.volumeId}/${suffix}${search ? `?${search}` : ''}`;
  }

  /** Every entry of a folder, following pages. */
  async list(folder: string): Promise<FileEntry[]> {
    const entries: FileEntry[] = [];
    let cursor: string | undefined;

    do {
      const page = listingSchema.parse(await (await this.send(this.route('files', { path: folder, cursor }))).json());
      entries.push(...page.entries);
      cursor = page.nextCursor;
    } while (cursor);

    return entries;
  }

  /**
   * Every file and folder below a folder that the filters let through, in path order, as the file
   * server finds them. The server stops one walk at an entry or time bound, and this pages on with
   * the cursor it ends the page with, so the caller sees every match however long the walk.
   *
   * A page whose stream ends before the server says it's done fails rather than pass for every
   * match; it isn't retried, since starting over would repeat what was already yielded. A page the
   * server stops without a cursor to continue from — it considered nothing, so nothing repeats — is
   * retried a few times before giving up.
   */
  async* find(folder: string, filters: FindFilters = {}): AsyncGenerator<FileEntry> {
    let cursor: string | undefined;
    let stalls = 0;

    for (;;) {
      const end = yield* this.findPage(folder, filters, cursor);

      if (end.done) {
        return;
      }

      if (!end.cursor || end.cursor === cursor) {
        if (++stalls > 2) {
          throw new CliError('The search kept stopping at the file server\'s bounds before it finished. Narrow it and run it again.');
        }

        continue;
      }

      cursor = end.cursor;
      stalls = 0;
    }
  }

  /** One page of a find stream, ended by its done line. */
  private async* findPage(folder: string, filters: FindFilters, cursor?: string): AsyncGenerator<FileEntry, FindEnd> {
    const response = await this.send(this.route('find', {
      path: folder,
      name: filters.name,
      ignoreCase: filters.ignoreCase ? 'true' : undefined,
      type: filters.type,
      maxDepth: filters.maxDepth?.toString(),
      minSize: filters.minSize?.toString(),
      maxSize: filters.maxSize?.toString(),
      modifiedAfter: filters.modifiedAfter?.toISOString(),
      modifiedBefore: filters.modifiedBefore?.toISOString(),
      cursor,
    }));

    const body = Readable.fromWeb(response.body as never).setEncoding('utf8');
    const cut = new CliError('The search stopped before it finished. Run it again.');
    let pending = '';

    async function* chunks() {
      try {
        for await (const chunk of body) {
          yield chunk as string;
        }
      } catch {
        throw cut;
      }
    }

    try {
      for await (const chunk of chunks()) {
        pending += chunk;
        let end: number;

        while ((end = pending.indexOf('\n')) >= 0) {
          const line = pending.slice(0, end).trim();
          pending = pending.slice(end + 1);

          if (!line) {
            continue;
          }

          const parsed = JSON.parse(line) as FindEnd & { error?: { code?: string; message?: string } };

          if (parsed.error) {
            throw new FileApiError(parsed.error.message ?? 'The search failed.', response.status, parsed.error.code);
          }

          if (parsed.done !== undefined) {
            if (parsed.done) {
              return { done: true };
            }

            return parsed.cursor ? { done: false, cursor: parsed.cursor } : { done: false };
          }

          yield fileEntrySchema.parse(parsed);
        }
      }
    } finally {
      body.destroy();
    }

    throw cut;
  }

  /** The entry at a path, or null when nothing is there. */
  async entry(target: string): Promise<FileEntry | null> {
    try {
      return fileEntrySchema.parse(await (await this.send(this.route('entry', { path: target }))).json());
    } catch (error) {
      if (error instanceof FileApiError && error.status === 404 && error.code === 'not_found') {
        return null;
      }

      throw error;
    }
  }

  async mkdir(target: string, parents: boolean): Promise<FileEntry> {
    const response = await this.send(this.route('directories'), {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ path: target, parents }),
    });

    return fileEntrySchema.parse(await response.json());
  }

  async remove(target: string, recursive: boolean): Promise<void> {
    await (await this.send(this.route('entry', { path: target, recursive: recursive ? 'true' : undefined }), { method: 'DELETE' })).body?.cancel();
  }

  async move(from: string, to: string, overwrite: boolean): Promise<FileEntry> {
    const response = await this.send(this.route('move'), {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ from, to, overwrite }),
    });

    return fileEntrySchema.parse(await response.json());
  }

  /**
   * Copies, or moves, a file from a volume of the same organization into this one on the file
   * server, creating missing folders. A long copy answers at once and sends spaces until its result.
   */
  async copyFrom(fromVolume: string, from: string, to: string, options: { overwrite: boolean; move?: boolean }): Promise<FileEntry> {
    const response = await this.send(this.route(options.move ? 'move' : 'copy'), {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({
        fromVolume, from, to, overwrite: options.overwrite, parents: true,
      }),
    });

    const result = JSON.parse((await response.text()).trim()) as { error?: { code?: string; message?: string } };

    if (result.error) {
      throw new FileApiError(result.error.message ?? 'The copy failed.', response.status, result.error.code);
    }

    return fileEntrySchema.parse(result);
  }

  // A lane upload keeps its lanes apart from url, so a CLI from before lanes starts over rather than resume a lane as the file.
  private async uploadState(): Promise<Record<string, { url?: string; lanes?: string[]; at: number }>> {
    try {
      const all = JSON.parse(await fs.readFile(this.stateFile, 'utf8')) as Record<string, { url?: string; lanes?: string[]; at: number }>;
      return Object.fromEntries(Object.entries(all).filter(([, value]) => Date.now() - value.at < UPLOAD_STATE_TTL_MS));
    } catch {
      return {};
    }
  }

  private async rememberUpload(key: string, upload: { url: string } | { lanes: string[] } | null): Promise<void> {
    const state = await this.uploadState();

    if (upload) {
      state[key] = { ...upload, at: Date.now() };
    } else {
      delete state[key];
    }

    await fs.mkdir(path.dirname(this.stateFile), { recursive: true });
    const temporary = `${this.stateFile}.${process.pid}.tmp`;
    await fs.writeFile(temporary, JSON.stringify(state), { mode: 0o600 });
    await fs.rename(temporary, this.stateFile);
  }

  /** How much of an upload the server holds, or null when it no longer exists. */
  private async offsetOf(location: string): Promise<number | null> {
    try {
      return Number((await this.send(location, { method: 'HEAD' })).headers.get('upload-offset') ?? 0);
    } catch (error) {
      if (error instanceof FileApiError && (error.status === 404 || error.status === 410)) {
        return null;
      }

      throw error;
    }
  }

  /**
   * Uploads one local file to `remote`. The file's modification time is kept.
   * `create` refuses to replace anything; `overwrite` replaces a file. An
   * `ifMatch` version — as the entry's `version` field read it — makes the
   * replacement refuse with `precondition_failed` unless the remote file is
   * still the one that version came from.
   */
  async upload(local: string, remote: string, options: {
    mode: UploadMode;
    parents: boolean;
    /** The remote entry's `version` the upload must still find, in `overwrite` mode. */
    ifMatch?: string;
    onProgress?: (sent: number) => void;
  }): Promise<void> {
    if (options.ifMatch !== undefined && options.mode !== 'overwrite') {
      throw new CliError('A version guard only guards an overwrite.');
    }

    const stat = await fs.stat(local);
    const size = stat.size;
    const progress = options.onProgress ?? (() => undefined);
    const key = [this.volumeId, remote, size, stat.mtimeMs, options.mode, options.ifMatch ?? ''].join('\n');

    const metadata = encodeMetadata({
      volume: this.volumeId,
      path: remote,
      mode: options.mode,
      ifMatch: options.ifMatch ? `"${options.ifMatch}"` : undefined,
      parents: String(options.parents),
      modifiedAt: stat.mtime.toISOString(),
    });

    let location: string | null;
    let offset: number | null;

    if (size <= INLINE_UPLOAD_BYTES) {
      const unfinished = await this.uploadInline(local, remote, size, stat.mtime, metadata);

      if (!unfinished) {
        progress(size);
        return;
      }

      ({ location, offset } = unfinished);
    } else {
      if (size > Math.min(this.chunkBytes, LANE_PIECE_BYTES) && await this.uploadLanes(local, size, key, metadata, progress)) {
        await this.rememberUpload(key, null);
        return;
      }

      location = (await this.uploadState())[key]?.url ?? null;
      offset = location ? await this.offsetOf(location) : null;
    }

    if (offset === null) {
      const created = await this.send('/v1/uploads', { method: 'POST', headers: { 'Upload-Length': String(size), 'Upload-Metadata': metadata } });
      location = created.headers.get('location');

      if (!location) {
        throw new CliError('The file service did not return an upload location.');
      }

      offset = 0;
      await this.rememberUpload(key, { url: location });
    }

    await this.sendFrom({
      location: location!, local, base: 0, length: size, offset, pieceBytes: this.chunkBytes, key,
    }, progress);

    await this.rememberUpload(key, null);
  }

  /**
   * Sends the file as lanes at once: partial uploads of its parts that the
   * server joins in place. False when the server doesn't confirm lanes; servers
   * before them ignore Upload-Concat and would make a lane a whole upload.
   */
  private async uploadLanes(local: string, size: number, key: string, metadata: string, progress: (sent: number) => void): Promise<boolean> {
    const pieceBytes = Math.min(this.chunkBytes, LANE_PIECE_BYTES);
    const count = Math.min(LANES, Math.ceil(size / pieceBytes));
    const laneBytes = Math.ceil(size / count);
    const bounds = Array.from({ length: count }, (_, index) => [index * laneBytes, Math.min((index + 1) * laneBytes, size)] as const);
    const sent = bounds.map(() => 0);

    const headers = (index: number, group?: string) => ({
      'Upload-Concat': 'partial',
      'Upload-Length': String(bounds[index]![1] - bounds[index]![0]),
      'Upload-Metadata': `${metadata},${encodeMetadata({ offset: String(bounds[index]![0]), ...(group ? { group } : {}) })}`,
    });

    let lanes = (await this.uploadState())[key]?.lanes ?? null;

    if (lanes?.length === count) {
      const held = await Promise.all(lanes.map(lane => this.offsetOf(lane)));

      if (held.every(offset => offset !== null)) {
        held.forEach((offset, index) => sent[index] = offset!);
      } else {
        lanes = null;
      }
    } else {
      lanes = null;
    }

    if (!lanes) {
      let first: Response;

      try {
        first = await this.once('/v1/uploads', { method: 'POST', headers: headers(0) });
      } catch {
        return false;
      }

      const location = first.headers.get('location');
      await first.body?.cancel();

      if (first.status !== 201 || !location || first.headers.get('upload-concat') !== 'partial') {
        if (first.status === 201 && location) {
          await this.once(location, { method: 'DELETE' }).then(response => response.body?.cancel(), () => undefined);
        }

        return false;
      }

      const group = location.slice(location.lastIndexOf('/') + 1);
      const rest = await Promise.all(bounds.slice(1).map((_, index) => this.send('/v1/uploads', { method: 'POST', headers: headers(index + 1, group) })));
      lanes = [location, ...rest.map(response => response.headers.get('location')!)];
      await this.rememberUpload(key, { lanes });
    }

    const report = (index: number) => (laneSent: number) => {
      sent[index] = laneSent;
      progress(sent.reduce((a, b) => a + b, 0));
    };

    // One lane failing stops the others, so none keeps sending after the upload gave up.
    const halt = new AbortController();

    await Promise.all(lanes.map((lane, index) => this.sendFrom({
      location: lane, local, base: bounds[index]![0], length: bounds[index]![1] - bounds[index]![0], offset: sent[index]!, pieceBytes, key,
    }, report(index), halt.signal).catch((error: unknown) => {
      halt.abort();
      throw error;
    })));

    try {
      await (await this.send('/v1/uploads', { method: 'POST', headers: { 'Upload-Concat': `final;${lanes.join(' ')}`, 'Upload-Metadata': metadata } })).body?.cancel();
    } catch (error) {
      if (error instanceof FileApiError && !RETRYABLE.has(error.status)) {
        await this.rememberUpload(key, null);
      }

      throw error;
    }

    return true;
  }

  /**
   * Sends bytes [base + offset, base + length) of `local` to the upload at
   * `location` in pieces, asking the server how much it kept after a dropped
   * connection or a retryable answer. `onSent` counts bytes of this upload.
   */
  private async sendFrom(part: { location: string; local: string; base: number; length: number; offset: number; pieceBytes: number; key: string }, onSent: (sent: number) => void, halt?: AbortSignal): Promise<void> {
    const {
      location, local, base, length, pieceBytes, key,
    } = part;

    let { offset } = part;

    for (let attempt = 0; offset < length;) {
      if (halt?.aborted) {
        throw new CliError('The upload stopped.');
      }

      const start: number = offset;
      let response: Response | null;
      const stalled = new AbortController();
      const stop = () => stalled.abort();
      halt?.addEventListener('abort', stop);
      let moved = Date.now();

      const watchdog = setInterval(() => {
        if (Date.now() - moved > this.stallMs) {
          stalled.abort();
        }
      }, Math.min(this.stallMs, 5_000));

      try {
        const end = base + Math.min(start + pieceBytes, length) - 1;

        const body = Readable.toWeb(createReadStream(local, { start: base + start, end }).pipe(counter((count) => {
          moved = Date.now();
          onSent(start + count);
        }))) as ReadableStream;

        response = await this.once(location, {
          method: 'PATCH',
          headers: { 'Upload-Offset': String(start), 'Content-Type': 'application/offset+octet-stream' },
          body,
          duplex: 'half',
          signal: stalled.signal,
        } as RequestInit);
      } catch {
        response = null;
      } finally {
        clearInterval(watchdog);
        halt?.removeEventListener('abort', stop);
      }

      if (halt?.aborted) {
        throw new CliError('The upload stopped.');
      }

      if (response?.status === 204) {
        offset = Number(response.headers.get('upload-offset') ?? start);
        attempt = 0;
        continue;
      }

      // 409: the server holds a different amount than was sent.
      if (response && !RETRYABLE.has(response.status) && response.status !== 409) {
        const error = await refusal(response);

        if (response.status === 404 || response.status === 410) {
          await this.rememberUpload(key, null);
        }

        throw error;
      }

      if (attempt >= RETRY_DELAYS_MS.length) {
        throw response ? await refusal(response) : new CliError('The upload kept losing its connection. Run the command again to resume.');
      }

      const retryAfter = response?.headers.get('retry-after');
      await response?.body?.cancel();
      await sleep(this.delay(attempt, retryAfter ? Number(retryAfter) : null));
      attempt += 1;

      const held = await this.offsetOf(location);

      if (held === null) {
        await this.rememberUpload(key, null);
        throw new CliError('The upload expired on the server. Run the command again to start over.');
      }

      offset = held;
      onSent(offset);
    }
  }

  /**
   * A small file goes in the creating request; a lost answer is checked
   * against what landed. Returns the upload when the server kept only part.
   */
  private async uploadInline(local: string, remote: string, size: number, modified: Date, metadata: string): Promise<{ location: string; offset: number } | null> {
    const content = await fs.readFile(local);

    for (let attempt = 0; ; attempt += 1) {
      let response: Response | null;

      try {
        response = await this.once('/v1/uploads', {
          method: 'POST',
          headers: { 'Upload-Length': String(size), 'Upload-Metadata': metadata, 'Content-Type': 'application/offset+octet-stream' },
          body: content,
        });
      } catch {
        response = null;
      }

      if (response?.status === 201) {
        const offset = Number(response.headers.get('upload-offset') ?? 0);
        const location = response.headers.get('location');
        await response.body?.cancel();

        if (offset === size) {
          return null;
        }

        if (location) {
          return { location, offset };
        }
      }

      if (response && !RETRYABLE.has(response.status) && response.status !== 201) {
        const error = await refusal(response);

        // An earlier attempt whose answer was lost may have placed this very file.
        if (attempt > 0 && error.code === 'already_exists') {
          const landed = await this.entry(remote);

          if (landed?.type === 'file' && landed.sizeBytes === size && Date.parse(landed.modifiedAt) === modified.getTime()) {
            return null;
          }
        }

        throw error;
      }

      if (attempt >= RETRY_DELAYS_MS.length) {
        throw response ? await refusal(response) : new CliError('The upload kept losing its connection. Run the command again.');
      }

      await response?.body?.cancel();
      await sleep(this.delay(attempt, null));
    }
  }

  /**
   * Downloads one file to `local`, through `<local>.<version>.partial` so an
   * interrupted download resumes in the next run and `local` only ever holds
   * a whole file. The remote modification time is kept.
   */
  async download(remote: FileEntry, local: string, onProgress?: (received: number) => void): Promise<void> {
    if (remote.type !== 'file' || !remote.version) {
      throw new CliError(`${remote.path} is not a file.`);
    }

    const progress = onProgress ?? (() => undefined);
    const partial = `${local}.${remote.version.slice(0, 12)}.partial`;
    const folder = path.dirname(local);
    await fs.mkdir(folder, { recursive: true });

    // Partial downloads of other versions of this file can't be resumed.
    for (const name of await fs.readdir(folder)) {
      if (name.startsWith(`${path.basename(local)}.`) && name.endsWith('.partial') && path.join(folder, name) !== partial) {
        await fs.rm(path.join(folder, name), { force: true });
      }
    }

    for (let attempt = 0; ; attempt += 1) {
      const have = await fs.stat(partial).then(stat => stat.size, () => 0);

      try {
        const response = await this.send(this.route('content', { path: remote.path }), {
          headers: have > 0 ? { Range: `bytes=${have}-`, 'If-Range': `"${remote.version}"` } : {},
        });

        // A whole response means the file changed since it was listed: start over.
        const append = have > 0 && response.status === 206;
        const expected = (append ? have : 0) + Number(response.headers.get('content-length') ?? Number.NaN);
        const base = append ? have : 0;

        await pipeline(
          Readable.fromWeb(response.body as never),
          counter(count => progress(base + count)),
          createWriteStream(partial, { flags: append ? 'a' : 'w' }),
        );

        const size = (await fs.stat(partial)).size;

        if (Number.isFinite(expected) && size < expected) {
          throw new TypeError('The download ended early.');
        }

        await fs.rename(partial, local);
        const modified = new Date(remote.modifiedAt);
        await fs.utimes(local, modified, modified);
        return;
      } catch (error) {
        if (error instanceof FileApiError && error.status === 416) {
          await fs.rm(partial, { force: true });
          continue;
        }

        if (error instanceof CliError || attempt >= RETRY_DELAYS_MS.length) {
          throw error;
        }

        await sleep(this.delay(attempt, null));
      }
    }
  }
}
