import { createServer, type IncomingMessage, type Server } from 'node:http';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { Command } from 'commander';
import {
  afterAll, afterEach, beforeAll, beforeEach, describe, expect, it, vi,
} from 'vitest';
import { FileApiError, FilesClient } from '../src/clients/files';
import {
  between, direction, download, find, listing, parseAddress, parseAge, parseSizes, progressLine, remotePath, under, upload,
} from '../src/commands/volume/files';
import { syncVolumeFolder } from '../src/commands/volume/sync';
import { CliContext } from '../src/context';

const VOLUME = '11111111-2222-4333-8444-555555555555';
const BACKUP = '22222222-2222-4333-8444-555555555555';

type StoredFile = { data: Buffer; modifiedAt: string; version: string };
type Upload = { path: string; size: number; data: Buffer[]; received: number; modifiedAt: string; partial?: boolean };

/** Just enough of the file API: uploads, ranged downloads, listings, and faults on request. */
class FakeFiles {
  files = new Map<string, StoredFile>();
  /** Another volume of the organization, for copies between volumes. */
  backup = new Map<string, StoredFile>();
  /** The next copy or move answers 200 at once, then fails with this code. */
  failNextCopyLate: string | null = null;
  /** How the next find ends after its matches: done, a clean end without done, a reset connection, or an error line. */
  findEnd: 'done' | 'end' | 'reset' | 'error' = 'done';
  /** When set, one find page holds this many matches, then ends with a cursor. */
  findPageAt: number | null = null;
  /** Makes find pages stop without a cursor, or repeat the same cursor, so the client gives up. */
  findStall: false | 'no-cursor' | 'same-cursor' = false;
  /** Folders made by mkdir, as the file server holds them. */
  dirs = new Set<string>();
  /** The Upload-Metadata of the last upload created. */
  lastMetadata: Record<string, string> | null = null;
  /** Versions listings report instead of the stored one, as a listing read before a change. */
  listVersions = new Map<string, string>();
  findQuery: URLSearchParams | null = null;
  uploads = new Map<string, Upload>();
  /** Joins lanes, as file servers since parallel uploads; older ones ignore Upload-Concat. */
  lanes = false;
  lanesCreated = 0;
  joins = 0;
  /** PATCHes still to answer 500. */
  failPatches = 0;
  /** The next PATCH is taken in whole and its answer never comes, as over a connection that stalls. */
  hangNextPatch = false;
  cutNextPatchAfter: number | null = null;
  cutNextDownloadAfter: number | null = null;
  /** Like Cloudflare's request body limit. */
  maxPatchBytes = Number.POSITIVE_INFINITY;
  /** Requests by method still to answer as a file server that is starting. */
  starting: Record<string, number> = {};
  /** Answers files_starting until a session newer than this one, as when a start takes too long. */
  stoppedSinceSession: number | null = null;
  expireNextToken = false;
  sessions = 0;
  requests: string[] = [];
  server!: Server;
  url = '';

  async start() {
    this.server = createServer((request, response) => {
      // A client that halts its other lanes drops their requests mid-body.
      this.handle(request, response).catch((error: NodeJS.ErrnoException) => {
        if (error.code !== 'ECONNRESET') {
          throw error;
        }
      });
    });

    await new Promise<void>(resolve => this.server.listen(0, '127.0.0.1', resolve));
    this.url = `http://127.0.0.1:${(this.server.address() as { port: number }).port}`;
  }

  async body(request: IncomingMessage, limit = Number.POSITIVE_INFINITY): Promise<{ data: Buffer; cut: boolean }> {
    const chunks: Buffer[] = [];
    let length = 0;

    for await (const chunk of request) {
      const buffer = chunk as Buffer;

      if (length + buffer.length >= limit) {
        chunks.push(buffer.subarray(0, limit - length));
        return { data: Buffer.concat(chunks), cut: true };
      }

      chunks.push(buffer);
      length += buffer.length;
    }

    return { data: Buffer.concat(chunks), cut: false };
  }

  volume(id: string | undefined): Map<string, StoredFile> {
    return id === BACKUP ? this.backup : this.files;
  }

  finish(upload: Upload) {
    if (upload.received === upload.size && !upload.partial) {
      this.files.set(upload.path, { data: Buffer.concat(upload.data), modifiedAt: upload.modifiedAt, version: `v${this.files.size}${Date.now()}` });
    }
  }

  async handle(request: IncomingMessage, response: import('node:http').ServerResponse) {
    const url = new URL(request.url!, this.url);
    this.requests.push(`${request.method} ${url.pathname}`);

    if (this.stoppedSinceSession !== null && this.sessions <= this.stoppedSinceSession) {
      response.writeHead(503, { 'retry-after': '5', 'content-type': 'application/json' }).end(JSON.stringify({ error: { code: 'files_starting', message: 'starting' } }));
      return;
    }

    if (this.starting[request.method!]) {
      this.starting[request.method!]! -= 1;
      response.writeHead(503, { 'retry-after': '5', 'content-type': 'application/json' }).end(JSON.stringify({ error: { code: 'files_starting', message: 'starting' } }));
      return;
    }

    if (this.expireNextToken) {
      this.expireNextToken = false;
      response.writeHead(401, { 'content-type': 'application/json' }).end(JSON.stringify({ error: { code: 'token_expired', message: 'expired' } }));
      return;
    }

    if (request.method === 'POST' && url.pathname === '/v1/uploads') {
      const metadata = Object.fromEntries(String(request.headers['upload-metadata']).split(',').map((pair) => {
        const [key, value] = pair.split(' ');
        return [key, Buffer.from(value!, 'base64').toString()];
      }));

      this.lastMetadata = metadata;

      const existing = this.files.get(metadata['path']!);

      if (metadata['mode'] === 'create' && existing) {
        response.writeHead(412, { 'content-type': 'application/json' }).end(JSON.stringify({ error: { code: 'already_exists', message: 'exists' } }));
        return;
      }

      if (metadata['ifMatch'] && (!existing || `"${existing.version}"` !== metadata['ifMatch'])) {
        response.writeHead(412, { 'content-type': 'application/json' }).end(JSON.stringify({ error: { code: 'precondition_failed', message: 'changed' } }));
        return;
      }

      const concat = this.lanes ? String(request.headers['upload-concat'] ?? '') : '';
      const id = `${VOLUME}.${this.uploads.size}`;

      if (concat.startsWith('final;')) {
        const lanes = concat.slice('final;'.length).trim().split(' ').map(lane => this.uploads.get(lane.slice(lane.lastIndexOf('/') + 1)));

        if (lanes.some(lane => !lane?.partial || lane.received !== lane.size)) {
          response.writeHead(400).end();
          return;
        }

        this.files.set(metadata['path']!, { data: Buffer.concat(lanes.flatMap(lane => lane!.data)), modifiedAt: metadata['modifiedAt']!, version: `v${this.files.size}${Date.now()}` });
        this.joins += 1;

        this.uploads.set(id, {
          path: metadata['path']!, size: 0, data: [], received: 0, modifiedAt: metadata['modifiedAt']!,
        });

        response.writeHead(201, { location: `${this.url}/v1/uploads/${id}` }).end();
        return;
      }

      const upload: Upload = {
        path: metadata['path']!, size: Number(request.headers['upload-length']), data: [], received: 0, modifiedAt: metadata['modifiedAt']!, partial: concat === 'partial',
      };

      this.uploads.set(id, upload);

      if (upload.partial) {
        this.lanesCreated += 1;
        response.setHeader('upload-concat', 'partial');
      }

      if (request.headers['content-type'] === 'application/offset+octet-stream') {
        const { data } = await this.body(request);
        upload.data.push(data);
        upload.received += data.length;
        this.finish(upload);
      }

      response.writeHead(201, { location: `${this.url}/v1/uploads/${id}`, 'upload-offset': String(upload.received) }).end();
      return;
    }

    const upload = this.uploads.get(url.pathname.replace('/v1/uploads/', ''));

    if (upload && request.method === 'HEAD') {
      response.writeHead(200, { 'upload-offset': String(upload.received) }).end();
      return;
    }

    if (upload && request.method === 'DELETE') {
      this.uploads.delete(url.pathname.replace('/v1/uploads/', ''));
      response.writeHead(204).end();
      return;
    }

    if (upload && request.method === 'PATCH' && this.hangNextPatch) {
      this.hangNextPatch = false;
      const { data } = await this.body(request);
      upload.data.push(data);
      upload.received += data.length;
      this.finish(upload);
      return;
    }

    if (upload && request.method === 'PATCH' && this.failPatches > 0) {
      this.failPatches -= 1;
      await this.body(request);
      response.writeHead(500).end();
      return;
    }

    if (upload && request.method === 'PATCH') {
      if (Number(request.headers['upload-offset']) !== upload.received) {
        response.writeHead(409).end();
        return;
      }

      const limit = this.cutNextPatchAfter;
      this.cutNextPatchAfter = null;
      const { data, cut } = await this.body(request, limit ?? Number.POSITIVE_INFINITY);

      if (data.length > this.maxPatchBytes) {
        response.writeHead(413).end();
        return;
      }

      upload.data.push(data);
      upload.received += data.length;

      if (cut) {
        response.destroy();
        return;
      }

      this.finish(upload);
      response.writeHead(204, { 'upload-offset': String(upload.received) }).end();
      return;
    }

    const filePath = url.searchParams.get('path') ?? '';
    const files = this.volume(url.pathname.split('/')[3]);

    if (request.method === 'POST' && url.pathname.endsWith('/directories')) {
      const body = JSON.parse((await this.body(request)).data.toString()) as { path: string; parents: boolean };

      if (files.has(body.path)) {
        response.writeHead(409, { 'content-type': 'application/json' }).end(JSON.stringify({ error: { code: 'not_a_directory', message: 'A file is in the way.' } }));
        return;
      }

      const parts = body.path.split('/');

      for (let depth = body.parents ? 1 : parts.length; depth <= parts.length; depth++) {
        this.dirs.add(parts.slice(0, depth).join('/'));
      }

      response.writeHead(201, { 'content-type': 'application/json' }).end(JSON.stringify({
        name: parts.at(-1), path: body.path, type: 'directory', modifiedAt: '2026-01-01T00:00:00.000Z',
      }));

      return;
    }

    if (request.method === 'POST' && /\/(copy|move)$/.test(url.pathname)) {
      const body = JSON.parse((await this.body(request)).data.toString()) as { fromVolume: string; from: string; to: string; overwrite: boolean; parents: boolean };
      const source = this.volume(body.fromVolume);
      const file = source.get(body.from);

      if (!file) {
        response.writeHead(404, { 'content-type': 'application/json' }).end(JSON.stringify({ error: { code: 'not_found', message: 'not found' } }));
        return;
      }

      if (!body.overwrite && files.has(body.to)) {
        response.writeHead(412, { 'content-type': 'application/json' }).end(JSON.stringify({ error: { code: 'already_exists', message: 'exists' } }));
        return;
      }

      // A long copy: the status and spaces go out before the result.
      response.writeHead(200, { 'content-type': 'application/json' });
      response.write('  ');

      if (this.failNextCopyLate) {
        response.end(JSON.stringify({ error: { code: this.failNextCopyLate, message: 'The volume is full.' } }));
        this.failNextCopyLate = null;
        return;
      }

      files.set(body.to, { ...file });

      if (url.pathname.endsWith('/move')) {
        source.delete(body.from);
      }

      response.end(JSON.stringify({
        name: body.to.split('/').at(-1), path: body.to, type: 'file', sizeBytes: file.data.length, modifiedAt: file.modifiedAt, version: file.version,
      }));

      return;
    }

    if (url.pathname.endsWith('/entry')) {
      const file = files.get(filePath);

      if (request.method === 'DELETE') {
        files.delete(filePath);
        response.writeHead(204).end();
        return;
      }

      const name = filePath.split('/').at(-1)!;
      const folder = this.dirs.has(filePath) || [...files.keys()].some(key => key.startsWith(`${filePath}/`));

      if (!file && !folder) {
        response.writeHead(404, { 'content-type': 'application/json' }).end(JSON.stringify({ error: { code: 'not_found', message: 'not found' } }));
        return;
      }

      response.writeHead(200, { 'content-type': 'application/json' }).end(JSON.stringify(file
        ? {
          name, path: filePath, type: 'file', sizeBytes: file.data.length, modifiedAt: file.modifiedAt, version: this.listVersions.get(filePath) ?? file.version,
        }
        : { name, path: filePath, type: 'directory', modifiedAt: '2026-01-01T00:00:00.000Z' }));

      return;
    }

    if (url.pathname.endsWith('/content')) {
      const file = files.get(filePath)!;
      const range = request.headers.range?.match(/bytes=(\d+)-/);
      const ranged = range && request.headers['if-range'] === `"${file.version}"`;
      const start = ranged ? Number(range[1]) : 0;
      const body = file.data.subarray(start);
      response.writeHead(ranged ? 206 : 200, { 'content-length': String(body.length), etag: `"${file.version}"` });
      const limit = this.cutNextDownloadAfter;
      this.cutNextDownloadAfter = null;

      if (limit !== null) {
        response.write(body.subarray(0, limit), () => response.destroy());
        return;
      }

      response.end(body);
      return;
    }

    if (url.pathname.endsWith('/find')) {
      this.findQuery = url.searchParams;
      const end = this.findEnd;
      this.findEnd = 'done';

      const names = [...files.keys()].sort().filter(name => !filePath || name.startsWith(`${filePath}/`));

      const after = url.searchParams.get('cursor')
        ? (JSON.parse(Buffer.from(url.searchParams.get('cursor')!, 'base64url').toString()) as { a: string }).a
        : '';

      const remaining = names.filter(name => name > after);
      const page = this.findPageAt === null ? remaining : remaining.slice(0, this.findPageAt);

      const lines = page.map((name) => {
        const file = files.get(name)!;
        return JSON.stringify({
          name: name.split('/').at(-1), path: name, type: 'file', sizeBytes: file.data.length, modifiedAt: file.modifiedAt, version: file.version,
        });
      });

      response.writeHead(200, { 'content-type': 'application/x-ndjson' });

      // An empty line first, as the file server keeps a long walk open.
      response.write(`\n${lines.map(line => `${line}\n`).join('')}`, () => {
        if (end === 'reset') {
          response.destroy();
          return;
        }

        const error = JSON.stringify({ error: { code: 'unavailable', message: 'File access is temporarily unavailable. Retry shortly.' } });
        const done: Record<string, string> = { done: '{"done":true}\n', end: '', error: `${error}\n` };
        const closing = done[end];

        if (this.findPageAt === null || end !== 'done') {
          response.end(closing);
          return;
        }

        if (this.findStall === 'no-cursor') {
          response.end('{"done":false}\n');
          return;
        }

        if (this.findStall === 'same-cursor' && after) {
          response.end(`${JSON.stringify({ done: false, cursor: url.searchParams.get('cursor')! })}\n`);
          return;
        }

        if (remaining.length > page.length) {
          response.end(`${JSON.stringify({ done: false, cursor: Buffer.from(JSON.stringify({ a: page.at(-1)! })).toString('base64url') })}\n`);
          return;
        }

        response.end(closing);
      });

      return;
    }

    if (url.pathname.endsWith('/files')) {
      const folders = new Set<string>(this.dirs);

      for (const name of files.keys()) {
        const parts = name.split('/');

        for (let depth = 1; depth < parts.length; depth++) {
          folders.add(parts.slice(0, depth).join('/'));
        }
      }

      const prefix = filePath ? `${filePath}/` : '';
      const children = new Map<string, StoredFile | null>();

      for (const folder of folders) {
        if (folder.startsWith(prefix) && !folder.slice(prefix.length).includes('/')) {
          children.set(folder.slice(prefix.length), null);
        }
      }

      for (const [name, file] of files) {
        if (name.startsWith(prefix) && !name.slice(prefix.length).includes('/')) {
          children.set(name.slice(prefix.length), file);
        }
      }

      const names = [...children.keys()].sort();
      const after = url.searchParams.get('cursor') ? Buffer.from(url.searchParams.get('cursor')!, 'base64url').toString() : '';
      const page = names.filter(name => name > after).slice(0, 2);
      const more = names.filter(name => name > after).length > 2;

      response.writeHead(200, { 'content-type': 'application/json' }).end(JSON.stringify({
        path: '',
        entries: page.map((name) => {
          const file = children.get(name)!;

          return file
            ? {
              name,
              path: `${prefix}${name}`,
              type: 'file',
              sizeBytes: file.data.length,
              modifiedAt: file.modifiedAt,
              version: this.listVersions.get(`${filePath}${filePath ? '/' : ''}${name}`) ?? file.version,
            }
            : { name, path: `${prefix}${name}`, type: 'directory', modifiedAt: '2026-01-01T00:00:00.000Z' };
        }),
        ...(more ? { nextCursor: Buffer.from(page.at(-1)!).toString('base64url') } : {}),
      }));

      return;
    }

    response.writeHead(404).end();
  }
}

describe('FilesClient', () => {
  const fake = new FakeFiles();
  let dir: string;
  let client: FilesClient;

  const cloud = {
    createFileSession: async () => {
      fake.sessions += 1;
      return { token: `token-${fake.sessions}`, expiresAt: new Date(Date.now() + 600_000).toISOString(), baseUrl: fake.url };
    },
  };

  beforeAll(() => fake.start());
  afterAll(() => new Promise<void>(resolve => fake.server.close(() => resolve())));

  beforeEach(async () => {
    dir = await fs.mkdtemp(path.join(os.tmpdir(), 'featherless-files-'));
    fake.files.clear();
    fake.backup.clear();
    fake.failNextCopyLate = null;
    fake.findEnd = 'done';
    fake.findQuery = null;
    fake.findPageAt = null;
    fake.findStall = false;
    fake.dirs.clear();
    fake.lastMetadata = null;
    fake.listVersions.clear();
    fake.uploads.clear();
    fake.requests = [];
    fake.sessions = 0;
    fake.maxPatchBytes = Number.POSITIVE_INFINITY;
    fake.starting = {};
    fake.stoppedSinceSession = null;
    fake.lanes = false;
    fake.lanesCreated = 0;
    fake.joins = 0;
    fake.failPatches = 0;
    fake.hangNextPatch = false;
    client = new FilesClient(cloud, VOLUME, { delay: () => 1, stateFile: path.join(dir, 'uploads.json') });
  });

  afterEach(() => fs.rm(dir, { recursive: true, force: true }));

  it('resumes a large upload from what the server kept when the connection drops', async () => {
    const local = path.join(dir, 'weights.bin');
    const content = Buffer.alloc(6 * 1024 * 1024, 3);
    content.write('end', content.length - 3);
    await fs.writeFile(local, content);
    fake.cutNextPatchAfter = 1_000_000;
    const progress: number[] = [];

    await client.upload(local, 'models/weights.bin', { mode: 'create', parents: true, onProgress: sent => progress.push(sent) });

    expect(fake.files.get('models/weights.bin')?.data.equals(content)).toBe(true);
    expect(fake.requests.filter(request => request.startsWith('HEAD'))).toHaveLength(1);
    expect(progress.at(-1)).toBe(content.length);
    await expect(fs.readFile(path.join(dir, 'uploads.json'), 'utf8')).resolves.toBe('{}');
  });

  it('gives up on a request that stops moving and resumes from what the server kept', async () => {
    const local = path.join(dir, 'weights.bin');
    const content = Buffer.alloc(6 * 1024 * 1024 + 7, 6);
    content.write('end', content.length - 3);
    await fs.writeFile(local, content);
    fake.hangNextPatch = true;
    client = new FilesClient(cloud, VOLUME, { delay: () => 1, stateFile: path.join(dir, 'uploads.json'), stallMs: 200 });

    await client.upload(local, 'weights.bin', { mode: 'create', parents: false });

    expect(fake.files.get('weights.bin')?.data.equals(content)).toBe(true);
    expect(fake.requests.filter(request => request.startsWith('HEAD'))).toHaveLength(1);
  });

  it('sends a large file in pieces under the request body limit', async () => {
    const local = path.join(dir, 'weights.bin');
    const content = Buffer.alloc(6 * 1024 * 1024 + 5, 4);
    content.write('end', content.length - 3);
    await fs.writeFile(local, content);
    fake.maxPatchBytes = 2 * 1024 * 1024;
    client = new FilesClient(cloud, VOLUME, { delay: () => 1, stateFile: path.join(dir, 'uploads.json'), chunkBytes: 2 * 1024 * 1024 });

    await client.upload(local, 'weights.bin', { mode: 'create', parents: false });

    expect(fake.files.get('weights.bin')?.data.equals(content)).toBe(true);
    expect(fake.requests.filter(request => request.startsWith('PATCH'))).toHaveLength(4);
  });

  describe('lanes', () => {
    // Every 4 KiB holds its own offset, so a lane joined out of order shows.
    const content = Buffer.alloc(7 * 1024 * 1024 + 5);

    for (let offset = 0; offset + 4 <= content.length; offset += 4096) {
      content.writeUInt32LE(offset, offset);
    }

    beforeEach(() => {
      client = new FilesClient(cloud, VOLUME, { delay: () => 1, stateFile: path.join(dir, 'uploads.json'), chunkBytes: 2 * 1024 * 1024 });
    });

    it('sends a large file as four lanes at once that the server joins', async () => {
      fake.lanes = true;
      await fs.writeFile(path.join(dir, 'weights.bin'), content);
      const progress: number[] = [];

      await client.upload(path.join(dir, 'weights.bin'), 'weights.bin', { mode: 'create', parents: false, onProgress: sent => progress.push(sent) });

      expect(fake.files.get('weights.bin')?.data.equals(content)).toBe(true);
      expect([fake.lanesCreated, fake.joins]).toEqual([4, 1]);
      expect(progress.at(-1)).toBe(content.length);
      await expect(fs.readFile(path.join(dir, 'uploads.json'), 'utf8')).resolves.toBe('{}');
    });

    it('sends the file in order to a server that does not confirm lanes', async () => {
      await fs.writeFile(path.join(dir, 'weights.bin'), content);

      await client.upload(path.join(dir, 'weights.bin'), 'weights.bin', { mode: 'create', parents: false });

      expect(fake.files.get('weights.bin')?.data.equals(content)).toBe(true);
      expect(fake.joins).toBe(0);
      // It made the first lane a whole upload, which is dropped.
      expect(fake.requests).toContain(`DELETE /v1/uploads/${VOLUME}.0`);
    });

    it('continues the lanes an earlier run left unfinished', async () => {
      fake.lanes = true;
      fake.failPatches = 1_000;
      await fs.writeFile(path.join(dir, 'weights.bin'), content);
      await expect(client.upload(path.join(dir, 'weights.bin'), 'weights.bin', { mode: 'create', parents: false })).rejects.toThrow();

      fake.failPatches = 0;
      await client.upload(path.join(dir, 'weights.bin'), 'weights.bin', { mode: 'create', parents: false });

      expect(fake.files.get('weights.bin')?.data.equals(content)).toBe(true);
      expect([fake.lanesCreated, fake.joins]).toEqual([4, 1]);
    });
  });

  it('waits for a file server that is starting, as long as it asks', async () => {
    const local = path.join(dir, 'weights.bin');
    const content = Buffer.alloc(6 * 1024 * 1024, 5);
    await fs.writeFile(local, content);
    fake.cutNextPatchAfter = 1_000_000;
    fake.starting = { POST: 1, HEAD: 1 };
    const waits: Array<number | null> = [];

    client = new FilesClient(cloud, VOLUME, {
      delay: (_attempt, retryAfter) => {
        waits.push(retryAfter);
        return 1;
      },
      stateFile: path.join(dir, 'uploads.json'),
    });

    await client.upload(local, 'weights.bin', { mode: 'create', parents: false });

    expect(fake.files.get('weights.bin')?.data.equals(content)).toBe(true);
    expect(waits.filter(wait => wait === 5)).toHaveLength(2);
  });

  it('gets a new session when the file server did not start in time', async () => {
    await fs.writeFile(path.join(dir, 'a.txt'), 'a');
    await client.upload(path.join(dir, 'a.txt'), 'a.txt', { mode: 'create', parents: false });
    fake.stoppedSinceSession = fake.sessions;

    expect((await client.list('')).map(entry => entry.name)).toEqual(['a.txt']);
    expect(fake.sessions).toBe(2);

    fake.stoppedSinceSession = fake.sessions;
    await fs.writeFile(path.join(dir, 'b.bin'), Buffer.alloc(6 * 1024 * 1024, 1));
    await client.upload(path.join(dir, 'b.bin'), 'b.bin', { mode: 'create', parents: false });
    expect(fake.files.get('b.bin')?.data.length).toBe(6 * 1024 * 1024);
    expect(fake.sessions).toBe(3);
  });

  it('continues an upload an earlier run left unfinished', async () => {
    const local = path.join(dir, 'data.bin');
    const content = Buffer.alloc(5 * 1024 * 1024, 9);
    await fs.writeFile(local, content);
    const stat = await fs.stat(local);

    fake.uploads.set(`${VOLUME}.0`, {
      path: 'data.bin', size: content.length, data: [content.subarray(0, 2_000_000)], received: 2_000_000, modifiedAt: stat.mtime.toISOString(),
    });

    const key = [VOLUME, 'data.bin', content.length, stat.mtimeMs, 'create', ''].join('\n');
    await fs.writeFile(path.join(dir, 'uploads.json'), JSON.stringify({ [key]: { url: `${fake.url}/v1/uploads/${VOLUME}.0`, at: Date.now() } }));

    await client.upload(local, 'data.bin', { mode: 'create', parents: false });

    expect(fake.files.get('data.bin')?.data.equals(content)).toBe(true);
    expect(fake.requests).not.toContain('POST /v1/uploads');
  });

  it('sends a small file with the request that creates it, keeping its modification time', async () => {
    const local = path.join(dir, 'notes.txt');
    await fs.writeFile(local, 'hello');
    await fs.utimes(local, new Date('2026-01-02T03:04:05Z'), new Date('2026-01-02T03:04:05Z'));

    await client.upload(local, 'notes.txt', { mode: 'create', parents: false });

    expect(fake.requests).toEqual(['POST /v1/uploads']);
    expect(fake.files.get('notes.txt')).toMatchObject({ data: Buffer.from('hello'), modifiedAt: '2026-01-02T03:04:05.000Z' });
  });

  it('guards an overwrite with the version it read', async () => {
    fake.files.set('notes.txt', { data: Buffer.from('old'), modifiedAt: '2026-01-01T00:00:00.000Z', version: 'aaaa1111' });
    const local = path.join(dir, 'notes.txt');
    await fs.writeFile(local, 'new');

    await client.upload(local, 'notes.txt', { mode: 'overwrite', parents: false, ifMatch: 'aaaa1111' });

    expect(fake.files.get('notes.txt')).toMatchObject({ data: Buffer.from('new') });
    expect(fake.lastMetadata).toMatchObject({ mode: 'overwrite', ifMatch: '"aaaa1111"' });
  });

  it('refuses an overwrite whose version no longer matches', async () => {
    fake.files.set('notes.txt', { data: Buffer.from('old'), modifiedAt: '2026-01-01T00:00:00.000Z', version: 'aaaa1111' });
    const local = path.join(dir, 'notes.txt');
    await fs.writeFile(local, 'new');

    await expect(client.upload(local, 'notes.txt', { mode: 'overwrite', parents: false, ifMatch: 'bbbb2222' }))
      .rejects.toMatchObject({ code: 'precondition_failed' });

    expect(fake.files.get('notes.txt')).toMatchObject({ data: Buffer.from('old') });
  });

  it('refuses a version guard on a create', async () => {
    const local = path.join(dir, 'notes.txt');
    await fs.writeFile(local, 'new');

    await expect(client.upload(local, 'notes.txt', { mode: 'create', parents: false, ifMatch: 'aaaa1111' }))
      .rejects.toThrow('version guard');

    expect(fake.files.has('notes.txt')).toBe(false);
  });

  it('resumes a dropped download with a range and only then shows the whole file', async () => {
    const content = Buffer.from('0123456789'.repeat(100_000));
    fake.files.set('big.txt', { data: content, modifiedAt: '2026-02-03T04:05:06.000Z', version: 'abc123def4567890' });
    fake.cutNextDownloadAfter = 300_000;
    const local = path.join(dir, 'out', 'big.txt');

    await client.download({
      name: 'big.txt', path: 'big.txt', type: 'file', sizeBytes: content.length, modifiedAt: '2026-02-03T04:05:06.000Z', version: 'abc123def4567890',
    }, local);

    await expect(fs.readFile(local)).resolves.toEqual(content);
    expect((await fs.stat(local)).mtime.toISOString()).toBe('2026-02-03T04:05:06.000Z');
    await expect(fs.readdir(path.join(dir, 'out'))).resolves.toEqual(['big.txt']);
    expect(fake.requests.filter(request => request.endsWith('/content'))).toHaveLength(2);
  });

  it('renews an expired token once and follows listing pages', async () => {
    for (const name of ['a', 'b', 'c', 'd', 'e']) {
      fake.files.set(name, { data: Buffer.from(name), modifiedAt: '2026-01-01T00:00:00.000Z', version: name });
    }

    fake.expireNextToken = true;
    const entries = await client.list('');

    expect(entries.map(entry => entry.name)).toEqual(['a', 'b', 'c', 'd', 'e']);
    expect(fake.sessions).toBe(2);
  });

  describe('find', () => {
    const stored = (text: string) => ({ data: Buffer.from(text), modifiedAt: '2026-01-02T03:04:05.000Z', version: text });

    const collect = async (folder: string) => {
      const found: string[] = [];

      try {
        for await (const entry of client.find(folder)) {
          found.push(entry.path);
        }
      } catch (error) {
        return { found, error: error as Error & { code?: string } };
      }

      return { found };
    };

    beforeEach(() => {
      fake.files.set('models/a.bin', stored('a'));
      fake.files.set('models/sub/b.bin', stored('bb'));
      fake.files.set('notes.txt', stored('n'));
    });

    it('yields each match the server streams and ends on its done line, sending the filters', async () => {
      const found: string[] = [];

      for await (const entry of client.find('models', {
        name: '*.bin', ignoreCase: true, type: 'file', maxDepth: 2, minSize: 0, modifiedAfter: new Date('2026-01-01T00:00:00Z'),
      })) {
        found.push(entry.path);
      }

      expect(found).toEqual(['models/a.bin', 'models/sub/b.bin']);

      expect(Object.fromEntries(fake.findQuery!)).toEqual({
        path: 'models', name: '*.bin', ignoreCase: 'true', type: 'file', maxDepth: '2', minSize: '0', modifiedAfter: '2026-01-01T00:00:00.000Z',
      });
    });

    it('fails rather than pass a stream that ended before done for every match', async () => {
      for (const end of ['end', 'reset'] as const) {
        fake.findEnd = end;
        const { found, error } = await collect('');

        expect(found).toEqual(['models/a.bin', 'models/sub/b.bin', 'notes.txt']);
        expect(error?.message).toBe('The search stopped before it finished. Run it again.');
      }
    });

    it('fails with the error the server sends after it started answering', async () => {
      fake.findEnd = 'error';
      const { found, error } = await collect('models');

      expect(found).toEqual(['models/a.bin', 'models/sub/b.bin']);
      expect(error).toBeInstanceOf(FileApiError);
      expect(error?.code).toBe('unavailable');
    });

    it('follows the cursors of pages the server stops early', async () => {
      fake.findPageAt = 1;
      const found: string[] = [];

      for await (const entry of client.find('')) {
        found.push(entry.path);
      }

      expect(found).toEqual(['models/a.bin', 'models/sub/b.bin', 'notes.txt']);
    });

    it('gives up when stopped pages keep coming without a cursor', async () => {
      fake.findPageAt = 1;
      fake.findStall = 'no-cursor';
      const { error } = await collect('');

      expect(error?.message).toContain('kept stopping');
    });

    it('gives up when the server keeps repeating the same cursor', async () => {
      fake.findPageAt = 1;
      fake.findStall = 'same-cursor';
      const { error } = await collect('');

      expect(error?.message).toContain('kept stopping');
    });

    it('prints vol:// addresses as they come, NUL-separated with print0, and a listing with long', async () => {
      const terminal = new CliContext(new Command());
      const data = { files: client, volumeName: 'data' };
      const stdout = vi.spyOn(process.stdout, 'write').mockImplementation(() => true);
      const written = () => stdout.mock.calls.map(call => String(call[0])).join('');

      try {
        await find(terminal, data, parseAddress('vol://data/models')!, {}, {});
        expect(written()).toBe('vol://data/models/a.bin\nvol://data/models/sub/b.bin\n');

        stdout.mockClear();
        await find(terminal, data, parseAddress('vol://data/models')!, {}, { print0: true });
        expect(written()).toBe('vol://data/models/a.bin\0vol://data/models/sub/b.bin\0');

        stdout.mockClear();
        await find(terminal, data, parseAddress('vol://data/models')!, {}, { long: true });

        expect(written()).toBe(`${listing([
          { at: '2026-01-02T03:04:05.000Z', bytes: 1, name: 'vol://data/models/a.bin' },
          { at: '2026-01-02T03:04:05.000Z', bytes: 2, name: 'vol://data/models/sub/b.bin' },
        ], false).join('\n')}\n`);
      } finally {
        stdout.mockRestore();
      }
    });
  });

  describe('cp and mv', () => {
    const ctx = new CliContext(new Command().option('--json').parse(['--json'], { from: 'user' }));
    let data: { files: FilesClient; volumeName: string };

    beforeEach(() => {
      data = { files: client, volumeName: 'data' };
    });

    it('copies into a folder under the source name, and onto the destination name otherwise', async () => {
      const a = path.join(dir, 'a.txt');
      const c = path.join(dir, 'c.txt');
      await fs.writeFile(a, 'a');
      await fs.writeFile(c, 'c');

      await upload(ctx, data, [a], parseAddress('vol://data/models/')!, { limit: 1 });
      await upload(ctx, data, [c], parseAddress('vol://data/models')!, { limit: 1 });
      await upload(ctx, data, [a], parseAddress('vol://data/b.txt')!, { limit: 1 });

      expect([...fake.files.keys()].sort()).toEqual(['b.txt', 'models/a.txt', 'models/c.txt']);
      await expect(upload(ctx, data, [a, c], parseAddress('vol://data/new')!, { limit: 1 })).rejects.toThrow('not a folder');
    });

    it('a move deletes each source only once its own copy finished', async () => {
      const kept = path.join(dir, 'kept.txt');
      const moved = path.join(dir, 'moved.txt');
      await fs.writeFile(kept, 'local');
      await fs.writeFile(moved, 'moved');
      fake.files.set('kept.txt', { data: Buffer.from('remote'), modifiedAt: '2026-01-01T00:00:00.000Z', version: 'k' });

      const up = await upload(ctx, data, [kept, moved], parseAddress('vol://data/')!, { limit: 1, move: true });

      expect(up.failed.map(item => item.source)).toEqual([kept]);
      await expect(fs.readFile(kept, 'utf8')).resolves.toBe('local');
      await expect(fs.stat(moved)).rejects.toThrow();
      expect(fake.files.get('moved.txt')?.data.toString()).toBe('moved');

      const out = path.join(dir, 'out');
      await fs.mkdir(out);
      const down = await download(ctx, data, [parseAddress('vol://data/moved.txt')!], out, { limit: 1, move: true });

      expect(down.done.map(item => item.source)).toEqual(['vol://data/moved.txt']);
      await expect(fs.readFile(path.join(out, 'moved.txt'), 'utf8')).resolves.toBe('moved');
      expect(fake.files.has('moved.txt')).toBe(false);
    });

    it('prints nothing on success, each file with verbose, and each failure on stderr', async () => {
      const terminal = new CliContext(new Command());
      const a = path.join(dir, 'a.txt');
      await fs.writeFile(a, 'a');
      const stdout = vi.spyOn(process.stdout, 'write').mockImplementation(() => true);
      const stderr = vi.spyOn(process.stderr, 'write').mockImplementation(() => true);
      const written = (spy: typeof stdout) => spy.mock.calls.map(call => String(call[0])).join('');

      try {
        await upload(terminal, data, [a], parseAddress('vol://data/')!, { limit: 1 });
        expect(written(stdout) + written(stderr)).toBe('');

        await upload(terminal, data, [a], parseAddress('vol://data/b.txt')!, { limit: 1, verbose: true });
        expect(written(stdout)).toBe(`${a} -> vol://data/b.txt\n`);

        const report = await upload(terminal, data, [a], parseAddress('vol://data/')!, { limit: 1 });
        expect(report.failed).toHaveLength(1);
        expect(written(stderr)).toBe(`${a}: already exists in the volume (pass --overwrite or --update)\n`);
      } finally {
        stdout.mockRestore();
        stderr.mockRestore();
      }
    });

    it('shows progress on stderr only when asked, ending with one finished line', async () => {
      const terminal = new CliContext(new Command());
      const a = path.join(dir, 'a.txt');
      await fs.writeFile(a, 'a');
      const stderr = vi.spyOn(process.stderr, 'write').mockImplementation(() => true);
      let now = Date.now();
      const clock = vi.spyOn(Date, 'now').mockImplementation(() => now += 6_000);
      let lines: string[];

      try {
        await upload(terminal, data, [a], parseAddress('vol://data/')!, { limit: 1, progress: true });
        lines = stderr.mock.calls.map(call => String(call[0]));
      } finally {
        clock.mockRestore();
        stderr.mockRestore();
      }

      expect(lines[0]).toMatch(/^Uploading 0\/1 files, 0 B of 1 B/);
      expect(lines.filter(line => line.startsWith('Uploading 1/1 files, 1 B of 1 B'))).toHaveLength(1);
    });

    describe('between volumes', () => {
      let backup: { files: FilesClient; volumeName: string };
      const stored = (text: string) => ({ data: Buffer.from(text), modifiedAt: '2026-01-02T03:04:05.000Z', version: text });

      beforeEach(() => {
        backup = { files: new FilesClient(cloud, BACKUP, { delay: () => 1, stateFile: path.join(dir, 'uploads.json') }), volumeName: 'backup' };
        fake.files.set('models/a.bin', stored('a'));
        fake.files.set('models/sub/b.bin', stored('b'));
      });

      it('copies a folder on the file server, keeping its layout, and never through this computer', async () => {
        const report = await between(ctx, { ...data, paths: [parseAddress('vol://data/models')!] }, backup, parseAddress('vol://backup/')!, { limit: 2, recursive: true });

        expect(report.done.map(item => item.target).sort()).toEqual(['vol://backup/models/a.bin', 'vol://backup/models/sub/b.bin']);
        expect([...fake.backup.keys()].sort()).toEqual(['models/a.bin', 'models/sub/b.bin']);
        expect(fake.files.size).toBe(2);
        expect(fake.requests.filter(request => request.includes('/uploads') || request.includes('/content'))).toEqual([]);
        expect(fake.requests).toContain(`POST /v1/volumes/${BACKUP}/copy`);
      });

      it('moves, deleting each source only once its copy finished, and skips unchanged files with --update', async () => {
        fake.backup.set('a.bin', stored('a'));
        const skipped = await between(ctx, { ...data, paths: [parseAddress('vol://data/models/a.bin')!] }, backup, parseAddress('vol://backup/')!, { limit: 1, update: true });
        expect(skipped.skipped.map(item => item.source)).toEqual(['vol://data/models/a.bin']);

        fake.files.set('models/c.bin', stored('c'));
        fake.backup.set('models/c.bin', stored('other'));
        const report = await between(ctx, { ...data, paths: [parseAddress('vol://data/models')!] }, backup, parseAddress('vol://backup/')!, { limit: 1, move: true });

        expect(report.failed.map(item => [item.source, item.error])).toEqual([['vol://data/models/c.bin', 'already exists in the destination (pass --overwrite)']]);
        expect([...fake.files.keys()]).toEqual(['models/c.bin']);
        expect(fake.backup.get('models/sub/b.bin')?.data.toString()).toBe('b');
        expect(fake.requests).toContain(`POST /v1/volumes/${BACKUP}/move`);
      });

      it('reports a failure the server sends after it started answering', async () => {
        fake.failNextCopyLate = 'volume_full';
        const report = await between(ctx, { ...data, paths: [parseAddress('vol://data/models/a.bin')!] }, backup, parseAddress('vol://backup/a.bin')!, { limit: 1 });

        expect(report.failed.map(item => item.error)).toEqual(['The volume is full.']);
        expect(fake.backup.size).toBe(0);
      });
    });
  });

  describe('sync', () => {
    const ctx = new CliContext(new Command().option('--json').parse(['--json'], { from: 'user' }));

    const data = {
      get files() {
        return client;
      },
      volumeName: 'data',
    };

    const stored = (text: string, modifiedAt: string, version: string) => ({ data: Buffer.from(text), modifiedAt, version });

    let local: string;

    const put = async (name: string, text: string, at: string) => {
      await fs.mkdir(path.dirname(path.join(local, name)), { recursive: true });
      await fs.writeFile(path.join(local, name), text);
      await fs.utimes(path.join(local, name), new Date(at), new Date(at));
    };

    beforeEach(() => {
      local = path.join(dir, 'local');
    });

    it('sends newer local files and receives newer remote ones', async () => {
      await put('a.txt', 'new a', '2026-02-01T00:00:00Z');
      fake.files.set('a.txt', stored('old a', '2026-01-01T00:00:00.000Z', 'v1'));
      fake.files.set('b.bin', stored('b data', '2026-01-05T00:00:00.000Z', 'v2'));

      const report = await syncVolumeFolder(ctx, data, local, '', {});

      expect(report.uploaded).toBe(1);
      expect(report.downloaded).toBe(1);
      expect(report.conflicts).toBe(0);
      // The replacement was guarded by the version the listing read.
      expect(fake.lastMetadata).toMatchObject({ mode: 'overwrite', ifMatch: '"v1"' });
      // Each side now holds both files, with their own modification times kept.
      expect(fake.files.get('a.txt')).toMatchObject({ data: Buffer.from('new a'), modifiedAt: '2026-02-01T00:00:00.000Z' });
      await expect(fs.readFile(path.join(local, 'b.bin'), 'utf8')).resolves.toBe('b data');
      expect((await fs.stat(path.join(local, 'b.bin'))).mtime.toISOString()).toBe('2026-01-05T00:00:00.000Z');
    });

    it('pairs nested folders by name, sending and receiving below the root', async () => {
      await put('sub/a.txt', 'new a', '2026-02-01T00:00:00Z');
      await put('sub/b.bin', 'old b', '2026-01-01T00:00:00Z');
      await put('sub/same.txt', 'same', '2026-02-01T00:00:00Z');
      await put('root.txt', 'root', '2026-02-01T00:00:00Z');
      fake.files.set('sub/a.txt', stored('old a', '2026-01-01T00:00:00.000Z', 'v1'));
      fake.files.set('sub/b.bin', stored('new b', '2026-03-01T00:00:00.000Z', 'v2'));
      fake.files.set('sub/same.txt', stored('same', '2026-02-01T00:00:00.000Z', 'v3'));
      fake.files.set('root.txt', stored('root', '2026-02-01T00:00:00.000Z', 'v4'));
      // An unrelated root file the nested download must not replace.
      await put('b.bin', 'root b', '2026-01-01T00:00:00Z');
      fake.files.set('b.bin', stored('root b', '2026-01-01T00:00:00.000Z', 'v5'));

      const report = await syncVolumeFolder(ctx, data, local, '', {});

      expect(report.uploaded).toBe(1);
      expect(report.downloaded).toBe(1);
      expect(report.skipped).toBe(3);
      expect(report.conflicts).toBe(0);
      expect(fake.files.get('sub/a.txt')).toMatchObject({ data: Buffer.from('new a') });
      await expect(fs.readFile(path.join(local, 'sub/b.bin'), 'utf8')).resolves.toBe('new b');
      await expect(fs.readFile(path.join(local, 'b.bin'), 'utf8')).resolves.toBe('root b');
      await expect(fs.readFile(path.join(local, 'root.txt'), 'utf8')).resolves.toBe('root');
    });

    it('calls a same-time size change below the root a conflict', async () => {
      await put('sub/a.txt', 'local copy', '2026-02-01T00:00:00Z');
      fake.files.set('sub/a.txt', stored('remote copy', '2026-02-01T00:00:00.000Z', 'v1'));

      const report = await syncVolumeFolder(ctx, data, local, '', {});

      expect(report.conflicts).toBe(1);
      expect(report.uploaded + report.downloaded).toBe(0);
      expect(fake.files.get('sub/a.txt')?.data.toString()).toBe('remote copy');
      await expect(fs.readFile(path.join(local, 'sub/a.txt'), 'utf8')).resolves.toBe('local copy');
    });

    it('reports a nested file name held by a remote folder', async () => {
      await put('sub/a.txt', 'x', '2026-02-01T00:00:00Z');
      fake.dirs.add('sub/a.txt');

      const report = await syncVolumeFolder(ctx, data, local, '', {});

      expect(report.conflicts).toBe(1);
      expect(report.uploaded + report.downloaded).toBe(0);
      expect(fake.files.has('sub/a.txt')).toBe(false);
    });

    it('refuses to replace a nested local symlink with a remote file', async () => {
      await put('sub/target.txt', 'x', '2026-02-01T00:00:00Z');
      await fs.symlink('target.txt', path.join(local, 'sub/link.txt'));
      fake.files.set('sub/link.txt', stored('remote', '2026-03-01T00:00:00.000Z', 'v1'));

      const report = await syncVolumeFolder(ctx, data, local, '', {});

      expect(report.conflicts).toBe(1);
      expect(report.downloaded).toBe(0);
      await expect(fs.readlink(path.join(local, 'sub/link.txt'))).resolves.toBe('target.txt');
    });

    it('skips files that already match on both sides', async () => {
      await put('a.txt', 'same', '2026-02-01T00:00:00Z');
      fake.files.set('a.txt', stored('same', '2026-02-01T00:00:00.000Z', 'v1'));

      const report = await syncVolumeFolder(ctx, data, local, '', {});

      expect(report.skipped).toBe(1);
      expect(report.uploaded + report.downloaded).toBe(0);
      expect(fake.requests.filter(request => request.endsWith('/uploads') || request.endsWith('/content'))).toHaveLength(0);
    });

    it('calls a same-time size change a conflict rather than a guess', async () => {
      await put('a.txt', 'local side', '2026-02-01T00:00:00Z');
      fake.files.set('a.txt', stored('remote side', '2026-02-01T00:00:00.000Z', 'v1'));

      const report = await syncVolumeFolder(ctx, data, local, '', {});

      expect(report.conflicts).toBe(1);
      expect(report.uploaded + report.downloaded).toBe(0);
      expect(fake.files.get('a.txt')?.data.toString()).toBe('remote side');
      await expect(fs.readFile(path.join(local, 'a.txt'), 'utf8')).resolves.toBe('local side');
    });

    it('refuses to replace a remote file that changed while syncing', async () => {
      await put('a.txt', 'new', '2026-03-02T00:00:00Z');
      fake.files.set('a.txt', stored('newer still', '2026-03-01T00:00:00.000Z', 'v2'));
      // The listing read a version the stored file has since moved past.
      fake.listVersions.set('a.txt', 'v1');

      const report = await syncVolumeFolder(ctx, data, local, '', {});

      expect(report.conflicts).toBe(1);
      expect(report.uploaded).toBe(0);
      expect(fake.files.get('a.txt')?.data.toString()).toBe('newer still');
    });

    it('replaces the older side with --force', async () => {
      await put('a.txt', 'old local', '2026-01-01T00:00:00Z');
      fake.files.set('a.txt', stored('new remote', '2026-02-01T00:00:00.000Z', 'v1'));

      const report = await syncVolumeFolder(ctx, data, local, '', { mode: 'download', force: true });

      expect(report.downloaded).toBe(1);
      await expect(fs.readFile(path.join(local, 'a.txt'), 'utf8')).resolves.toBe('new remote');
    });

    it('needs --force to name a side', async () => {
      await expect(syncVolumeFolder(ctx, data, local, '', { force: true })).rejects.toThrow('--force needs');
    });

    it('names the winning side of a same-time change with --force, identical files still skip', async () => {
      await put('same-time.txt', 'local', '2026-02-01T00:00:00Z');
      await put('identical.txt', 'same', '2026-02-01T00:00:00Z');
      fake.files.set('same-time.txt', stored('remote', '2026-02-01T00:00:00.000Z', 'v1'));
      fake.files.set('identical.txt', stored('same', '2026-02-01T00:00:00.000Z', 'v2'));

      const report = await syncVolumeFolder(ctx, data, local, '', { mode: 'upload', force: true });

      expect(report.uploaded).toBe(1);
      expect(report.skipped).toBe(1);
      expect(fake.files.get('same-time.txt')).toMatchObject({ data: Buffer.from('local') });
    });

    it('names a side with --force when the remote file has no timestamp', async () => {
      await put('a.txt', 'local', '2026-02-01T00:00:00Z');
      fake.files.set('a.txt', stored('remote', 'not a time', 'v1'));

      const report = await syncVolumeFolder(ctx, data, local, '', { mode: 'upload', force: true });

      expect(report.uploaded).toBe(1);
      expect(fake.files.get('a.txt')).toMatchObject({ data: Buffer.from('local') });
    });

    it('creates missing remote folders with --mkdirs, empty ones too', async () => {
      await put('sub/a.txt', 'x', '2026-02-01T00:00:00Z');
      await fs.mkdir(path.join(local, 'empty'));

      const report = await syncVolumeFolder(ctx, data, local, 'models', { mkdirs: true });

      expect(report.uploaded).toBe(1);
      expect(report.directories).toBe(3);
      expect(fake.files.has('models/sub/a.txt')).toBe(true);
      expect(fake.dirs.has('models')).toBe(true);
      expect(fake.dirs.has('models/sub')).toBe(true);
      expect(fake.dirs.has('models/empty')).toBe(true);
    });

    it('refuses a missing remote folder without --mkdirs', async () => {
      await put('a.txt', 'x', '2026-02-01T00:00:00Z');

      await expect(syncVolumeFolder(ctx, data, local, 'models', {})).rejects.toThrow('does not exist');
    });

    it('creates the local folder for downloads with --mkdirs', async () => {
      fake.files.set('models/a.txt', stored('a', '2026-02-01T00:00:00.000Z', 'v1'));
      const out = path.join(dir, 'out');

      const report = await syncVolumeFolder(ctx, data, out, 'models', { mode: 'download', mkdirs: true });

      expect(report.downloaded).toBe(1);
      expect(report.directories).toBe(1);
      await expect(fs.readFile(path.join(out, 'a.txt'), 'utf8')).resolves.toBe('a');
    });

    it('refuses a missing local folder without --mkdirs', async () => {
      fake.files.set('models/a.txt', stored('a', '2026-02-01T00:00:00.000Z', 'v1'));
      const out = path.join(dir, 'out');

      await expect(syncVolumeFolder(ctx, data, out, 'models', { mode: 'download' })).rejects.toThrow('--mkdirs');
    });

    it('sends one way only, leaving the other side alone', async () => {
      await put('a.txt', 'local only', '2026-02-01T00:00:00Z');
      fake.files.set('b.bin', stored('remote only', '2026-02-01T00:00:00.000Z', 'v1'));

      const report = await syncVolumeFolder(ctx, data, local, '', { mode: 'download' });

      expect(report.downloaded).toBe(1);
      expect(report.uploaded).toBe(0);
      expect(fake.files.has('a.txt')).toBe(false);
      await expect(fs.readFile(path.join(local, 'a.txt'), 'utf8')).resolves.toBe('local only');
      await expect(fs.readFile(path.join(local, 'b.bin'), 'utf8')).resolves.toBe('remote only');
    });

    it('only syncs paths the include and exclude globs let through', async () => {
      await put('models/a.bin', 'x', '2026-02-01T00:00:00Z');
      await put('models/notes.txt', 'x', '2026-02-01T00:00:00Z');
      await put('other/c.bin', 'x', '2026-02-01T00:00:00Z');

      const report = await syncVolumeFolder(ctx, data, local, '', { include: ['models/**'], exclude: ['**/*.txt'], mkdirs: true });

      expect(report.uploaded).toBe(1);
      expect(fake.files.has('models/a.bin')).toBe(true);
      expect(fake.files.has('models/notes.txt')).toBe(false);
      expect(fake.files.has('other/c.bin')).toBe(false);
    });

    it('skips an excluded folder whole, not just its files', async () => {
      await put('node_modules/pkg/a.bin', 'x', '2026-02-01T00:00:00Z');
      await put('keep.bin', 'y', '2026-02-01T00:00:00Z');

      const report = await syncVolumeFolder(ctx, data, local, 'remote', { exclude: ['node_modules/**'], mkdirs: true });

      expect(report.uploaded).toBe(1);
      expect(report.conflicts).toBe(0);
      expect(fake.files.has('remote/keep.bin')).toBe(true);
      expect(fake.dirs.has('remote/node_modules')).toBe(false);
    });

    it('does not prune a folder on a children-only glob', async () => {
      await put('logs/today/a.log', 'x', '2026-02-01T00:00:00Z');
      await put('logs/keep.txt', 'x', '2026-02-01T00:00:00Z');
      await put('logs/sub/keep.txt', 'x', '2026-02-01T00:00:00Z');

      const report = await syncVolumeFolder(ctx, data, local, 'remote', { exclude: ['logs/*'], mkdirs: true });

      expect(report.uploaded).toBe(2);
      expect(fake.files.has('remote/logs/sub/keep.txt')).toBe(true);
      expect(fake.files.has('remote/logs/today/a.log')).toBe(true);
      expect(fake.files.has('remote/logs/keep.txt')).toBe(false);
    });

    it('does not create folders an include glob cannot reach', async () => {
      await put('models/a.bin', 'x', '2026-02-01T00:00:00Z');
      await put('other/b.bin', 'y', '2026-02-01T00:00:00Z');
      await put('top.txt', 'z', '2026-02-01T00:00:00Z');

      const report = await syncVolumeFolder(ctx, data, local, 'remote', { include: ['models/**'], mkdirs: true });

      expect(report.uploaded).toBe(1);
      expect(fake.files.has('remote/models/a.bin')).toBe(true);
      expect(fake.dirs.has('remote/models')).toBe(true);
      expect(fake.dirs.has('remote/other')).toBe(false);
    });

    it('leaves remote staging file names out of the sync', async () => {
      await put('a.txt', 'x', '2026-02-01T00:00:00Z');
      fake.files.set('b.bin.abcdef123456.partial', stored('half', '2026-03-01T00:00:00.000Z', 'v1'));

      const report = await syncVolumeFolder(ctx, data, local, '', {});

      expect(report.uploaded).toBe(1);
      expect(report.skipped).toBe(1);
      expect(report.conflicts).toBe(0);
      expect(await fs.stat(path.join(local, 'b.bin.abcdef123456.partial')).catch(() => null)).toBeNull();
    });

    it('shows what it would do without writing', async () => {
      await put('a.txt', 'new', '2026-02-01T00:00:00Z');
      fake.files.set('a.txt', stored('old', '2026-01-01T00:00:00.000Z', 'v1'));

      const report = await syncVolumeFolder(ctx, data, local, '', { dryRun: true });

      expect(report.planned).toBe(1);
      expect(report.uploaded).toBe(0);
      expect(fake.files.get('a.txt')?.data.toString()).toBe('old');
      expect(report.actions).toEqual([{ path: 'a.txt', action: 'would-upload' }]);
    });

    it('reports a folder name held by a remote file, deleting nothing', async () => {
      await put('sub/a.txt', 'x', '2026-02-01T00:00:00Z');
      fake.files.set('sub', stored('a file named sub', '2026-01-01T00:00:00.000Z', 'v1'));

      const report = await syncVolumeFolder(ctx, data, local, '', {});

      expect(report.conflicts).toBe(1);
      expect(fake.files.has('sub/a.txt')).toBe(false);
      expect(fake.files.get('sub')?.data.toString()).toBe('a file named sub');
    });

    it('leaves interrupted download staging files out of the sync', async () => {
      await put('a.txt.abcdef123456.partial', 'half a file', '2026-02-01T00:00:00Z');

      const report = await syncVolumeFolder(ctx, data, local, '', {});

      expect(report.uploaded).toBe(0);
      expect(report.skipped).toBe(0);
      expect(fake.files.size).toBe(0);
    });

    it('skips local symlinks and special files', async () => {
      await put('a.txt', 'x', '2026-02-01T00:00:00Z');
      await fs.symlink('a.txt', path.join(local, 'link.txt'));

      const report = await syncVolumeFolder(ctx, data, local, '', {});

      expect(report.uploaded).toBe(1);
      expect(report.skipped).toBe(1);
      expect(report.actions.some(action => action.reason === 'A local symlink or special file is not synchronized.')).toBe(true);
    });
  });
});

describe('listing', () => {
  it('prints lines like aws s3 ls: local time, size with folders blank, name', () => {
    const at = new Date(2026, 9, 6, 21, 1, 30).toISOString();
    const time = '2026-10-06 21:01:30';

    const lines = [
      { at, name: './' },
      { at, name: 'sub/' },
      { at, bytes: 2, name: 'b.txt' },
      { at, bytes: 104_857_600, name: 'big.bin' },
    ];

    expect(listing(lines, false)).toEqual([
      `${time}${' '.repeat(13)}./`,
      `${time}${' '.repeat(13)}sub/`,
      `${time}${' '.repeat(10)}2  b.txt`,
      `${time}  104857600  big.bin`,
    ]);

    expect(listing(lines, true)).toEqual([
      `${time}${' '.repeat(13)}./`,
      `${time}${' '.repeat(13)}sub/`,
      `${time}${' '.repeat(8)}2 B  b.txt`,
      `${time}  100.0 MiB  big.bin`,
    ]);
  });
});

describe('progressLine', () => {
  it('counts files and bytes, with the rate once a second has passed', () => {
    expect(progressLine('Uploading', { files: 12, bytes: 1.2 * 1024 ** 3 }, { files: 40, bytes: 4 * 1024 ** 3 }, 14_400)).toBe('Uploading 12/40 files, 1.2 GiB of 4.0 GiB, 85.3 MiB/s');
    expect(progressLine('Downloading', { files: 0, bytes: 0 }, { files: 3, bytes: 2048 }, 0)).toBe('Downloading 0/3 files, 0 B of 2.0 KiB');
  });
});

describe('parseAddress', () => {
  it('reads vol://<volume>/<path> and leaves everything else local', () => {
    expect(parseAddress('./weights')).toBeNull();
    expect(parseAddress('C:\\data')).toBeNull();
    expect(parseAddress('vol://data')).toEqual({ volume: 'data', path: '', folder: true });
    expect(parseAddress('vol://data/models/')).toEqual({ volume: 'data', path: 'models', folder: true });
    expect(parseAddress('vol://data//models/a.bin')).toEqual({ volume: 'data', path: 'models/a.bin', folder: false });
    expect(() => parseAddress('vol://data/a/../b')).toThrow('not a valid volume path');
  });
});

describe('direction', () => {
  it('copies toward whichever side is a volume', () => {
    expect(direction(['./a', './b', 'vol://v/x/'])).toMatchObject({ kind: 'upload', sources: ['./a', './b'], destination: { volume: 'v', path: 'x' } });
    expect(direction(['vol://v/a', '.'])).toMatchObject({ kind: 'download', sources: [{ volume: 'v', path: 'a' }], destination: '.' });
    expect(direction(['vol://v/a', 'vol://v/b'])).toMatchObject({ kind: 'within' });
  });

  it('refuses what is not a copy between this computer and a volume', () => {
    expect(() => direction(['vol://v/a'])).toThrow('a source and a destination');
    expect(() => direction(['./a', './b'])).toThrow('Neither side is a volume');
    expect(() => direction(['./a', 'vol://v/a', 'vol://v/'])).toThrow('all on this computer or all in a volume');
    expect(() => direction(['vol:///a', '.'])).toThrow('not a volume path');
  });
});

describe('under', () => {
  it('keeps the source name inside a folder, and copies a volume root as its contents', () => {
    expect(under(true, 'weights', 'a/b.bin')).toBe('weights/a/b.bin');
    expect(under(false, 'weights', 'a/b.bin')).toBe('a/b.bin');
    expect(under(false, 'a.bin', '')).toBe('');
    expect(under(true, '', 'a/b.bin')).toBe('a/b.bin');
  });
});

describe('remotePath', () => {
  it('drops the slashes people type and refuses dot segments', () => {
    expect(remotePath('/data//models/')).toBe('data/models');
    expect(remotePath(undefined)).toBe('');
    expect(() => remotePath('data/../other')).toThrow('not a valid volume path');
  });
});

describe('parseSizes', () => {
  it('reads find size bounds in 1024-based units, keeping the narrowest range', () => {
    expect(parseSizes([])).toEqual({});
    expect(parseSizes(['+10G'])).toEqual({ minSize: 10 * 1024 ** 3 });
    expect(parseSizes(['-512k', '+1.5MiB', '+100'])).toEqual({ minSize: 1.5 * 1024 ** 2, maxSize: 512 * 1024 });
    expect(parseSizes(['-2G', '-1G'])).toEqual({ maxSize: 1024 ** 3 });
    expect(() => parseSizes(['10G'])).toThrow('not a size bound');
    expect(() => parseSizes(['+10X'])).toThrow('not a size bound');
  });
});

describe('parseAge', () => {
  it('reads a number and a unit', () => {
    expect(parseAge('30m')).toBe(30 * 60_000);
    expect(parseAge('1.5h')).toBe(90 * 60_000);
    expect(parseAge('7d')).toBe(7 * 86_400_000);
    expect(() => parseAge('7')).toThrow('not an age');
    expect(() => parseAge('yesterday')).toThrow('not an age');
  });
});
