import fs from 'node:fs/promises';
import path from 'node:path';
import { Option, type Command } from 'commander';
import type { CliCommand, Completer } from '../../completions';
import { createContext, type CliContext } from '../../context';
import { resolveAuth } from '../../config';
import { CloudClient } from '../../clients/cloud';
import {
  FileApiError, FilesClient, type FileEntry, type FindFilters,
} from '../../clients/files';
import { CliError, describeError } from '../../error';
import { findVolume, profiles, requireDeletion, UUID_PATTERN } from '../shared';

const MAX_CONCURRENCY = 8;
const MAX_VOLUMES = 3;
/** Longest typed prefix that still completes to the scheme, e.g. `vol` → `vol://`. */
const SCHEME_PREFIX = 3;
export const SCHEME = 'vol://';
const ADDRESS_HINT = 'Write volume paths as vol://<volume>/<path>.';

/** A volume path as people type it: leading and trailing slashes are dropped. */
export function remotePath(value: string | undefined): string {
  const cleaned = (value ?? '').replace(/^\/+|\/+$/g, '').replace(/\/{2,}/g, '/');

  if (cleaned.split('/').some(part => part === '..' || part === '.')) {
    throw new CliError(`"${value}" is not a valid volume path.`);
  }

  return cleaned;
}

/** `vol` → `vol://`, then volume names, then folder entries; empty for local paths so the shell completes files. */
export const volumePaths: Completer = async (word, ctx) => {
  if (word.length >= SCHEME_PREFIX && word.length < SCHEME.length && SCHEME.startsWith(word)) {
    return [SCHEME];
  }

  if (!word.startsWith(SCHEME)) {
    return [];
  }

  const cloud = await ctx.cloud();
  const all = await cloud.listVolumes();
  const rest = word.slice(SCHEME.length);
  const slash = rest.indexOf('/');

  if (slash === -1) {
    const needle = rest.toLowerCase();

    return all
      .filter(volume => volume.name.toLowerCase().startsWith(needle) || volume.id.startsWith(needle))
      .map(volume => `${SCHEME}${volume.name}/`);
  }

  const volumeRef = rest.slice(0, slash);
  const pathPartial = rest.slice(slash + 1);
  const exact = all.filter(volume => volume.name === volumeRef);
  const matched = exact.length ? exact : all.filter(volume => volume.name.toLowerCase().includes(volumeRef.toLowerCase()));

  if (UUID_PATTERN.test(volumeRef)) {
    const byId = all.find(volume => volume.id === volumeRef.toLowerCase() && !matched.includes(volume));

    if (byId) {
      matched.push(byId);
    }
  }

  let folder: string;
  let partial: string;

  if (pathPartial.endsWith('/')) {
    folder = remotePath(pathPartial);
    partial = '';
  } else {
    const cut = pathPartial.lastIndexOf('/');

    if (cut === -1) {
      folder = '';
      partial = pathPartial;
    } else {
      folder = remotePath(pathPartial.slice(0, cut + 1));
      partial = pathPartial.slice(cut + 1);
    }
  }

  const candidates: string[] = [];

  for (const volume of matched.slice(0, MAX_VOLUMES)) {
    let entries: FileEntry[];

    try {
      entries = await new FilesClient(cloud, volume.id).list(folder);
    } catch {
      continue;
    }

    for (const entry of entries) {
      if (entry.name.startsWith(partial)) {
        candidates.push(`${SCHEME}${volume.name}/${folder ? `${folder}/` : ''}${entry.name}${entry.type === 'directory' ? '/' : ''}`);
      }
    }
  }

  return [...new Set(candidates)];
};

export interface VolumeAddress {
  volume: string;
  path: string;
  /** The volume root, or written with a trailing slash. */
  folder: boolean;
}

/** `vol://<volume>/<path>`, or null for a path on this computer. */
export function parseAddress(value: string): VolumeAddress | null {
  if (!value.startsWith(SCHEME)) {
    return null;
  }

  const rest = value.slice(SCHEME.length);
  const slash = rest.indexOf('/');
  const raw = slash === -1 ? '' : rest.slice(slash + 1);
  const cleaned = remotePath(raw);

  return { volume: slash === -1 ? rest : rest.slice(0, slash), path: cleaned, folder: !cleaned || raw.endsWith('/') };
}

export function volumeAddress(value: string): VolumeAddress {
  const address = parseAddress(value);

  if (!address?.volume) {
    throw new CliError(`"${value}" is not a volume path. ${ADDRESS_HINT}`);
  }

  return address;
}

const shown = (volumeName: string, target: string) => `${SCHEME}${volumeName}/${target}`;

export type Direction =
  | { kind: 'upload'; sources: string[]; destination: VolumeAddress }
  | { kind: 'download'; sources: VolumeAddress[]; destination: string }
  | { kind: 'within'; sources: VolumeAddress[]; destination: VolumeAddress };

/** Which way a cp or mv goes; the last path is the destination. */
export function direction(paths: string[]): Direction {
  if (paths.length < 2) {
    throw new CliError('Name a source and a destination.');
  }

  const addresses = paths.map(value => value.startsWith(SCHEME) ? volumeAddress(value) : null);
  const destination = addresses.at(-1);
  const remote = addresses.slice(0, -1).filter((item): item is VolumeAddress => item !== null);

  if (remote.length && remote.length !== paths.length - 1) {
    throw new CliError('Sources must be all on this computer or all in a volume.');
  }

  if (!destination && !remote.length) {
    throw new CliError(`Neither side is a volume. ${ADDRESS_HINT}`);
  }

  if (!destination) {
    return { kind: 'download', sources: remote, destination: paths.at(-1)! };
  }

  return remote.length ? { kind: 'within', sources: remote, destination } : { kind: 'upload', sources: paths.slice(0, -1), destination };
}

/**
 * A copied file's path below the destination, by cp's rules: into a folder
 * each source keeps its own name, otherwise the one source takes the
 * destination's. A volume root has no name, so its contents are copied.
 */
export function under(intoFolder: boolean, sourceName: string, rest: string): string {
  return [intoFolder ? sourceName : '', rest].filter(Boolean).join('/');
}

function requireFolder(intoFolder: boolean, sources: number, destination: string): void {
  if (!intoFolder && sources > 1) {
    throw new CliError(`${destination} is not a folder. Several sources need a folder; end it with /.`);
  }
}

const join = (...parts: string[]) => parts.filter(Boolean).join('/');

export function formatSize(bytes: number): string {
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];
  let value = bytes;
  let unit = 0;

  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit += 1;
  }

  return `${unit === 0 ? Math.round(value) : value.toFixed(1)} ${units[unit]}`;
}

const SIZE_UNITS: Record<string, number> = {
  '': 1, K: 1024, M: 1024 ** 2, G: 1024 ** 3, T: 1024 ** 4,
};

/** `--size` bounds as find writes them: +N for more than N, -N for less, in bytes or K, M, G and T. */
export function parseSizes(values: string[]): Pick<FindFilters, 'minSize' | 'maxSize'> {
  const bounds: Pick<FindFilters, 'minSize' | 'maxSize'> = {};

  for (const value of values) {
    const match = value.match(/^([+-])(\d+(?:\.\d+)?)([KMGT]?)(?:i?B)?$/i);

    if (!match) {
      throw new CliError(`"${value}" is not a size bound. Write +N for more than N or -N for less, like +10G or -512K.`);
    }

    const bytes = Math.round(Number(match[2]) * SIZE_UNITS[match[3]!.toUpperCase()]!);

    if (match[1] === '+') {
      bounds.minSize = Math.max(bounds.minSize ?? 0, bytes);
    } else {
      bounds.maxSize = Math.min(bounds.maxSize ?? Number.MAX_SAFE_INTEGER, bytes);
    }
  }

  return bounds;
}

const AGE_UNITS: Record<string, number> = {
  s: 1_000, m: 60_000, h: 3_600_000, d: 86_400_000, w: 604_800_000,
};

/** An age like 30m, 12h or 7d, in milliseconds. */
export function parseAge(value: string): number {
  const match = value.match(/^(\d+(?:\.\d+)?)([smhdw])$/);

  if (!match) {
    throw new CliError(`"${value}" is not an age. Write a number and a unit, like 30m, 12h or 7d.`);
  }

  return Number(match[1]) * AGE_UNITS[match[2]!]!;
}

interface ListingLine {
  at: string;
  /** Absent for folders. */
  bytes?: number;
  name: string;
}

/** Lines like `aws s3 ls`: local time, size (blank for folders), name. */
export function listing(lines: ListingLine[], human: boolean): string[] {
  const pad = (n: number) => String(n).padStart(2, '0');

  const rows = lines.map((line) => {
    const at = new Date(line.at);
    const size = human ? formatSize(line.bytes ?? 0) : String(line.bytes ?? 0);

    return {
      at: `${at.getFullYear()}-${pad(at.getMonth() + 1)}-${pad(at.getDate())} ${pad(at.getHours())}:${pad(at.getMinutes())}:${pad(at.getSeconds())}`,
      size: line.bytes === undefined ? '' : size,
      name: line.name,
    };
  });

  const width = Math.max(0, ...rows.map(row => row.size.length));
  return rows.map(row => `${row.at}  ${row.size.padStart(width)}  ${row.name}`);
}

interface Count {
  files: number;
  bytes: number;
}

export function progressLine(verb: string, done: Count, total: Count, elapsedMs: number): string {
  const rate = elapsedMs >= 1000 ? `, ${formatSize(done.bytes / (elapsedMs / 1000))}/s` : '';
  return `${verb} ${done.files}/${total.files} files, ${formatSize(done.bytes)} of ${formatSize(total.bytes)}${rate}`;
}

const print = (stream: NodeJS.WriteStream, line: string) => stream.write(`${line}\n`);

/** JSON with --json; otherwise plain lines, as filesystem commands print them. */
function emit<T>(ctx: CliContext, data: T, lines: () => string[]): void {
  if (ctx.mode === 'json') {
    console.log(JSON.stringify(data, null, 2));
    return;
  }

  for (const line of lines()) {
    print(process.stdout, line);
  }
}

export interface VolumeFiles {
  files: FilesClient;
  volumeName: string;
}

/** One file client for the volume every reference names. */
export async function filesFor(refs: string[], profile: string | undefined, acrossVolumes = 'Name paths in one volume at a time.'): Promise<VolumeFiles> {
  const auth = await resolveAuth(profile);
  const cloud = new CloudClient(auth.apiBase, auth.apiKey);
  const volumes = await cloud.listVolumes();
  const found = [...new Set(refs)].map(ref => findVolume(volumes, ref));

  if (found.some(volume => volume.id !== found[0]!.id)) {
    throw new CliError(acrossVolumes);
  }

  return { files: new FilesClient(cloud, found[0]!.id), volumeName: found[0]!.name };
}

export function concurrency(value: string | undefined): number {
  const parsed = Number(value ?? 4);

  if (!Number.isInteger(parsed) || parsed < 1 || parsed > MAX_CONCURRENCY) {
    throw new CliError(`--concurrency must be 1 to ${MAX_CONCURRENCY}.`);
  }

  return parsed;
}

async function eachConcurrently<T>(items: T[], limit: number, work: (item: T) => Promise<void>): Promise<void> {
  let next = 0;

  await Promise.all(Array.from({ length: Math.min(limit, items.length) }, async () => {
    while (next < items.length) {
      const item = items[next++]!;
      await work(item);
    }
  }));
}

interface Transfer {
  source: string;
  target: string;
  bytes: number;
}

interface TransferReport {
  done: Transfer[];
  skipped: Transfer[];
  failed: Array<Transfer & { error: string }>;
}

export interface CopyOptions {
  limit: number;
  recursive?: boolean;
  overwrite?: boolean;
  update?: boolean;
  dryRun?: boolean;
  /** Delete each source once its copy finished. */
  move?: boolean;
  /** Print each file as it finishes. */
  verbose?: boolean;
  /** Show a running total on stderr. */
  progress?: boolean;
}

/**
 * Runs transfers like cp: silent on success, each failure on stderr, each
 * file with verbose, and a running total on stderr with progress, redrawn in
 * place on a terminal and every few seconds otherwise.
 */
async function runTransfers<T extends Transfer>(ctx: CliContext, verb: string, transfers: T[], options: CopyOptions, move: (transfer: T, progress: (bytes: number) => void) => Promise<void>): Promise<TransferReport> {
  const report: TransferReport = { done: [], skipped: [], failed: [] };
  const json = ctx.mode === 'json';
  const total = transfers.reduce((sum, item) => sum + item.bytes, 0);
  const moved = new Map<Transfer, number>();
  const started = Date.now();
  const terminal = Boolean(process.stderr.isTTY);
  const live = Boolean(options.progress) && !json && transfers.length > 0;
  let shownAt = 0;
  let drawn = false;
  let shownLast = false;

  const show = (force = false) => {
    if (!live || shownLast || (!force && Date.now() - shownAt < (terminal ? 250 : 5_000))) {
      return;
    }

    shownAt = Date.now();
    shownLast = report.done.length + report.failed.length === transfers.length;
    const done = { files: report.done.length, bytes: [...moved.values()].reduce((a, b) => a + b, 0) };
    const line = progressLine(verb, done, { files: transfers.length, bytes: total }, Date.now() - started);

    if (terminal) {
      process.stderr.write(`\r\x1b[K${line}`);
      drawn = true;
    } else {
      print(process.stderr, line);
    }
  };

  const say = (stream: NodeJS.WriteStream, line: string) => {
    if (drawn) {
      process.stderr.write('\r\x1b[K');
      drawn = false;
    }

    print(stream, line);
  };

  show(true);

  await eachConcurrently(transfers, options.limit, async (transfer) => {
    try {
      await move(transfer, (bytes) => {
        moved.set(transfer, bytes);
        show();
      });

      moved.set(transfer, transfer.bytes);
      report.done.push(transfer);

      if (options.verbose && !json) {
        say(process.stdout, `${transfer.source} -> ${transfer.target}`);
      }
    } catch (error) {
      report.failed.push({ ...transfer, error: describeError(error) });

      if (!json) {
        say(process.stderr, `${transfer.source}: ${describeError(error)}`);
      }
    }

    show();
  });

  show(true);

  if (drawn) {
    process.stderr.write('\n');
  }

  return report;
}

function finish(ctx: CliContext, report: TransferReport, dryRun: boolean): void {
  emit(ctx, report, () => dryRun
    ? [...report.done.map(item => `would copy ${item.source} -> ${item.target}`), ...report.skipped.map(item => `would skip ${item.source} (unchanged)`)]
    : []);

  if (report.failed.length) {
    process.exitCode = 1;
  }
}

/** Every file under a local path, with its path below that path ('' for a file). */
async function localFiles(source: string, recursive: boolean): Promise<{ folder: boolean; files: Array<{ local: string; rest: string; size: number; mtimeMs: number }> }> {
  const stat = await fs.stat(source).catch(() => null);

  if (!stat) {
    throw new CliError(`${source} does not exist.`);
  }

  if (stat.isFile()) {
    return { folder: false, files: [{ local: source, rest: '', size: stat.size, mtimeMs: stat.mtimeMs }] };
  }

  if (!stat.isDirectory()) {
    throw new CliError(`${source} is not a file or folder.`);
  }

  if (!recursive) {
    throw new CliError(`${source} is a folder. Pass -r to copy folders.`);
  }

  const found: Array<{ local: string; rest: string; size: number; mtimeMs: number }> = [];
  const root = path.resolve(source);

  const walk = async (folder: string) => {
    for (const entry of await fs.readdir(folder, { withFileTypes: true })) {
      const full = path.join(folder, entry.name);

      if (entry.isDirectory()) {
        await walk(full);
      } else if (entry.isFile()) {
        const info = await fs.stat(full);
        found.push({ local: full, rest: path.relative(root, full).split(path.sep).join('/'), size: info.size, mtimeMs: info.mtimeMs });
      }
    }
  };

  await walk(root);
  return { folder: true, files: found };
}

/** Every file under a volume path, with its path below that path ('' for a file). */
async function remoteFiles(files: FilesClient, source: string, recursive: boolean): Promise<{ folder: boolean; files: Array<{ entry: FileEntry; rest: string }> }> {
  const entry = source ? await files.entry(source) : null;

  if (source && !entry) {
    throw new CliError(`${source} does not exist in the volume.`);
  }

  if (entry?.type === 'file') {
    return { folder: false, files: [{ entry, rest: '' }] };
  }

  if (!recursive) {
    throw new CliError(`${source || 'The volume root'} is a folder. Pass -r to copy folders.`);
  }

  const found: Array<{ entry: FileEntry; rest: string }> = [];
  const prefix = source ? `${source}/` : '';

  const walk = async (folder: string) => {
    for (const child of await files.list(folder)) {
      if (child.type === 'directory') {
        await walk(child.path);
      } else {
        found.push({ entry: child, rest: child.path.slice(prefix.length) });
      }
    }
  };

  await walk(source);
  return { folder: true, files: found };
}

/** Removes the folders a move emptied, deepest first; folders still holding anything stay. */
async function removeEmptyLocal(folder: string): Promise<void> {
  for (const entry of await fs.readdir(folder, { withFileTypes: true })) {
    if (entry.isDirectory()) {
      await removeEmptyLocal(path.join(folder, entry.name));
    }
  }

  await fs.rmdir(folder).catch(() => undefined);
}

async function removeEmptyRemote(files: FilesClient, folder: string): Promise<void> {
  for (const child of await files.list(folder)) {
    if (child.type === 'directory') {
      await removeEmptyRemote(files, child.path);
    }
  }

  await files.remove(folder, false).catch((error: unknown) => {
    if (!(error instanceof FileApiError && error.code === 'directory_not_empty')) {
      throw error;
    }
  });
}

/** Copies files from this computer into a volume. */
export async function upload(ctx: CliContext, { files, volumeName }: VolumeFiles, sources: string[], destination: VolumeAddress, options: CopyOptions): Promise<TransferReport> {
  const existing = destination.folder ? null : await files.entry(destination.path);
  const intoFolder = destination.folder || existing?.type === 'directory';
  requireFolder(intoFolder, sources.length, shown(volumeName, destination.path));
  const planned: Array<Transfer & { remote: string; mtimeMs: number }> = [];
  const folders: string[] = [];

  for (const source of sources) {
    const found = await localFiles(source, Boolean(options.recursive || options.move));

    if (found.folder) {
      folders.push(source);
    }

    for (const file of found.files) {
      const remote = join(destination.path, under(intoFolder, path.basename(path.resolve(source)), file.rest));

      planned.push({
        source: file.local, target: shown(volumeName, remote), remote, bytes: file.size, mtimeMs: file.mtimeMs,
      });
    }
  }

  let pending = planned;
  const skipped: Transfer[] = [];

  if (options.update) {
    pending = [];

    await eachConcurrently(planned, options.limit, async (item) => {
      const existing = await files.entry(item.remote);
      const same = existing?.type === 'file' && existing.sizeBytes === item.bytes && Date.parse(existing.modifiedAt) === Math.floor(item.mtimeMs);
      (same ? skipped : pending).push(item);
    });
  }

  if (options.dryRun) {
    return { done: pending, skipped, failed: [] };
  }

  const replace = Boolean(options.overwrite || options.update);

  const report = await runTransfers(ctx, 'Uploading', pending, options, async (item, progress) => {
    await files.upload(item.source, item.remote, {
      mode: replace ? 'overwrite' : 'create',
      parents: true,
      onProgress: progress,
    }).catch((error: unknown) => {
      if (error instanceof FileApiError && error.code === 'already_exists') {
        throw new CliError(`already exists in the volume (pass --overwrite${options.move ? '' : ' or --update'})`);
      }

      throw error;
    });

    if (options.move) {
      await fs.rm(item.source);
    }
  });

  if (options.move) {
    for (const folder of folders) {
      await removeEmptyLocal(folder);
    }
  }

  return { ...report, skipped };
}

/** Copies files from a volume to this computer. */
export async function download(ctx: CliContext, { files, volumeName }: VolumeFiles, sources: VolumeAddress[], destination: string, options: CopyOptions): Promise<TransferReport> {
  const existing = await fs.stat(destination).catch(() => null);
  const intoFolder = /[\\/]$/.test(destination) || Boolean(existing?.isDirectory());
  requireFolder(intoFolder, sources.length, destination);
  const planned: Array<Transfer & { entry: FileEntry }> = [];
  const folders: string[] = [];

  for (const source of sources) {
    const found = await remoteFiles(files, source.path, Boolean(options.recursive || options.move));

    if (found.folder) {
      folders.push(source.path);
    }

    for (const file of found.files) {
      const target = path.join(destination, under(intoFolder, source.path.split('/').at(-1)!, file.rest));
      planned.push({ source: shown(volumeName, file.entry.path), target, bytes: file.entry.sizeBytes ?? 0, entry: file.entry });
    }
  }

  let pending = planned;
  const skipped: Transfer[] = [];

  if (options.update) {
    pending = [];

    for (const item of planned) {
      const local = await fs.stat(item.target).catch(() => null);
      const same = local?.isFile() && local.size === item.bytes && Math.floor(local.mtimeMs) === Date.parse(item.entry.modifiedAt);
      (same ? skipped : pending).push(item);
    }
  }

  if (options.dryRun) {
    return { done: pending, skipped, failed: [] };
  }

  const report = await runTransfers(ctx, 'Downloading', pending, options, async (item, progress) => {
    await files.download(item.entry, item.target, progress);

    if (options.move) {
      await files.remove(item.entry.path, false);
    }
  });

  if (options.move) {
    for (const folder of folders) {
      await removeEmptyRemote(files, folder);
    }
  }

  return { ...report, skipped };
}

/** Copies or moves files from one volume into another on the file server; nothing passes through this computer. */
export async function between(ctx: CliContext, source: VolumeFiles & { paths: VolumeAddress[] }, target: VolumeFiles, destination: VolumeAddress, options: CopyOptions): Promise<TransferReport> {
  const existing = destination.folder ? null : await target.files.entry(destination.path);
  const intoFolder = destination.folder || existing?.type === 'directory';
  requireFolder(intoFolder, source.paths.length, shown(target.volumeName, destination.path));
  const planned: Array<Transfer & { entry: FileEntry; remote: string }> = [];
  const folders: string[] = [];

  for (const address of source.paths) {
    const found = await remoteFiles(source.files, address.path, Boolean(options.recursive || options.move));

    if (found.folder) {
      folders.push(address.path);
    }

    for (const file of found.files) {
      const remote = join(destination.path, under(intoFolder, address.path.split('/').at(-1)!, file.rest));

      planned.push({
        source: shown(source.volumeName, file.entry.path), target: shown(target.volumeName, remote), remote, bytes: file.entry.sizeBytes ?? 0, entry: file.entry,
      });
    }
  }

  let pending = planned;
  const skipped: Transfer[] = [];

  if (options.update) {
    pending = [];

    await eachConcurrently(planned, options.limit, async (item) => {
      const existing = await target.files.entry(item.remote);
      const same = existing?.type === 'file' && existing.sizeBytes === item.bytes && existing.modifiedAt === item.entry.modifiedAt;
      (same ? skipped : pending).push(item);
    });
  }

  if (options.dryRun) {
    return { done: pending, skipped, failed: [] };
  }

  const report = await runTransfers(ctx, options.move ? 'Moving' : 'Copying', pending, options, async (item) => {
    await target.files.copyFrom(source.files.volumeId, item.entry.path, item.remote, {
      overwrite: Boolean(options.overwrite || options.update),
      move: options.move,
    }).catch((error: unknown) => {
      if (error instanceof FileApiError && error.code === 'already_exists') {
        throw new CliError(`already exists in the destination (pass --overwrite${options.move ? '' : ' or --update'})`);
      }

      // The file service only copies between volumes it can reach together.
      if (error instanceof FileApiError && error.code === 'volume_not_found') {
        throw new CliError(`${source.volumeName} and ${target.volumeName} can't be copied between directly. Copy the files to this computer, then into the other volume.`);
      }

      throw error;
    });
  });

  if (options.move) {
    for (const folder of folders) {
      await removeEmptyRemote(source.files, folder);
    }
  }

  return { ...report, skipped };
}

/**
 * Prints each match as a vol:// address as the file server finds it, or, with `long` or --json,
 * every match once the search finished.
 */
export async function find(ctx: CliContext, data: VolumeFiles, address: VolumeAddress, filters: FindFilters, options: { long?: boolean; human?: boolean; print0?: boolean }): Promise<void> {
  const name = (entry: FileEntry) => `${shown(data.volumeName, entry.path)}${entry.type === 'directory' ? '/' : ''}`;

  if (ctx.mode !== 'json' && !options.long) {
    // A reader like head may close the pipe early; stop searching then rather than fail.
    let closed = false;

    const onError = (error: NodeJS.ErrnoException) => {
      if (error.code !== 'EPIPE') {
        throw error;
      }

      closed = true;
    };

    process.stdout.on('error', onError);

    try {
      for await (const entry of data.files.find(address.path, filters)) {
        if (closed) {
          break;
        }

        process.stdout.write(`${name(entry)}${options.print0 ? '\0' : '\n'}`);
      }
    } finally {
      process.stdout.off('error', onError);
    }

    return;
  }

  const entries: FileEntry[] = [];

  for await (const entry of data.files.find(address.path, filters)) {
    entries.push(entry);
  }

  emit(ctx, entries, () => listing(entries.map(entry => (entry.type === 'directory' ? { at: entry.modifiedAt, name: name(entry) } : { at: entry.modifiedAt, bytes: entry.sizeBytes ?? 0, name: name(entry) })), Boolean(options.human)));
}

export function registerVolumeFileCommands(volume: CliCommand): void {
  volume
    .command('ls')
    .argument('[path]', 'vol://<volume>/<path> folder', { complete: volumePaths })
    .description('List your volumes, or a folder in one (vol://<volume>/<path>)')
    .option('-h, --human-readable', 'sizes like 1.5 KiB and 100.0 MiB instead of bytes')
    .option('-r, --recursive', 'list everything below the folder, by path from the volume root')
    .option('--summarize', 'end with the number of files and their total size')
    // Listings always show details and hidden files; these keep `ls -al` habits working.
    .addOption(new Option('-a, --all').hideHelp())
    .addOption(new Option('-l, --long').hideHelp())
    .option('--profile <name>', 'profile to use', { complete: profiles })
    .action(async (target: string | undefined, options: {
      humanReadable?: boolean; recursive?: boolean; summarize?: boolean; profile?: string;
    }, cmd: Command) => {
      const ctx = createContext(cmd);
      const human = Boolean(options.humanReadable);

      if (!target || target === SCHEME) {
        const auth = await resolveAuth(options.profile);
        const volumes = await new CloudClient(auth.apiBase, auth.apiKey).listVolumes();
        emit(ctx, volumes, () => listing(volumes.map(volume => ({ at: volume.createdAt, bytes: volume.capacityGiB * 1024 ** 3, name: `${SCHEME}${volume.name}` })), human));
        return;
      }

      const address = volumeAddress(target);
      const { files } = await filesFor([address.volume], options.profile);
      const line = (item: FileEntry, name: string): ListingLine => (item.type === 'directory' ? { at: item.modifiedAt, name: `${name}/` } : { at: item.modifiedAt, bytes: item.sizeBytes ?? 0, name });
      const entries: FileEntry[] = [];
      const lines: ListingLine[] = [];

      if (options.recursive) {
        const walk = async (folder: string) => {
          for (const child of await files.list(folder)) {
            entries.push(child);

            if (child.type === 'directory') {
              await walk(child.path);
            }
          }
        };

        await walk(address.path);
        lines.push(...entries.map(item => line(item, item.path)));
      } else {
        const parent = address.path.includes('/') ? address.path.slice(0, address.path.lastIndexOf('/')) : '';
        const [listed, self, above] = await Promise.all([files.list(address.path), files.entry(address.path), address.path ? files.entry(parent) : null]);
        entries.push(...listed);

        for (const [item, name] of [[self, '.'], [above, '..']] as const) {
          if (item) {
            lines.push(line(item, name));
          }
        }

        lines.push(...entries.map(item => line(item, item.name)));
      }

      emit(ctx, entries, () => {
        if (!options.summarize) {
          return listing(lines, human);
        }

        const sizes = entries.filter(item => item.type === 'file').map(item => item.sizeBytes ?? 0);
        const total = sizes.reduce((sum, size) => sum + size, 0);
        return [...listing(lines, human), '', `Total Files: ${sizes.length}`, ` Total Size: ${human ? formatSize(total) : total}`];
      });
    });

  volume
    .command('find')
    .argument('<path>', 'vol://<volume>/<path> folder to search', { complete: volumePaths })
    .description('Find files and folders below a folder in a volume (vol://<volume>/<path>), printing each as it is found')
    .option('--name <pattern>', 'names matching a pattern like "*.safetensors": * matches any characters, ? one, [abc] one of those')
    .option('--iname <pattern>', 'like --name, ignoring case')
    .option('--type <f|d>', 'only files (f) or only folders (d)', { choices: ['f', 'd'] })
    .option('--maxdepth <n>', 'how many levels below the folder to look')
    .option('--size <+n|-n>', 'more (+) or less (-) than a size like 10G or 512K; repeat for a range', (value: string, previous: string[] | undefined) => [...(previous ?? []), value])
    .option('--newer-than <age>', 'modified within an age like 30m, 12h or 7d')
    .option('--older-than <age>', 'modified longer ago than an age like 30m, 12h or 7d')
    .option('-l, --long', 'print each with its modification time and size, like ls')
    .option('-h, --human-readable', 'with -l, sizes like 1.5 KiB and 100.0 MiB instead of bytes')
    .option('-0, --print0', 'end each with a NUL instead of a newline, for xargs -0')
    .option('--profile <name>', 'profile to use', { complete: profiles })
    .action(async (target: string, options: {
      name?: string; iname?: string; type?: 'f' | 'd'; maxdepth?: string; size?: string[]; newerThan?: string; olderThan?: string;
      long?: boolean; humanReadable?: boolean; print0?: boolean; profile?: string;
    }, cmd: Command) => {
      const ctx = createContext(cmd);
      const maxDepth = options.maxdepth === undefined ? undefined : Number(options.maxdepth);

      if (options.name !== undefined && options.iname !== undefined) {
        throw new CliError('Pass --name or --iname, not both.');
      }

      if (maxDepth !== undefined && (!Number.isInteger(maxDepth) || maxDepth < 1)) {
        throw new CliError('--maxdepth takes a whole number of levels, 1 or more.');
      }

      const address = volumeAddress(target);
      const now = Date.now();

      const filters: FindFilters = {
        name: options.name ?? options.iname,
        ignoreCase: options.iname !== undefined,
        type: options.type === undefined ? undefined : ({ f: 'file', d: 'directory' } as const)[options.type],
        maxDepth,
        ...parseSizes(options.size ?? []),
        modifiedAfter: options.newerThan === undefined ? undefined : new Date(now - parseAge(options.newerThan)),
        modifiedBefore: options.olderThan === undefined ? undefined : new Date(now - parseAge(options.olderThan)),
      };

      const data = await filesFor([address.volume], options.profile);
      await find(ctx, data, address, filters, { long: options.long, human: options.humanReadable, print0: options.print0 });
    });

  volume
    .command('mkdir')
    .argument('<paths...>', 'vol:// folders to create', { complete: volumePaths })
    .description('Create folders in a volume')
    .option('-p, --parents', 'create missing parent folders')
    .option('--profile <name>', 'profile to use', { complete: profiles })
    .action(async (targets: string[], options: { parents?: boolean; profile?: string }, cmd: Command) => {
      const ctx = createContext(cmd);
      const addresses = targets.map(volumeAddress);
      const { files, volumeName } = await filesFor(addresses.map(address => address.volume), options.profile);

      const created: FileEntry[] = [];

      for (const address of addresses) {
        created.push(await files.mkdir(address.path, Boolean(options.parents)));
      }

      emit(ctx, created, () => []);
    });

  volume
    .command('rm')
    .argument('<paths...>', 'vol:// files or folders', { complete: volumePaths })
    .description('Delete files or folders in a volume')
    .option('-r, --recursive', 'delete folders and everything in them')
    .option('--yes', 'skip confirmation')
    .option('--profile <name>', 'profile to use', { complete: profiles })
    .action(async (targets: string[], options: { recursive?: boolean; yes?: boolean; profile?: string }, cmd: Command) => {
      const ctx = createContext(cmd);
      const addresses = targets.map(volumeAddress);

      if (addresses.some(address => !address.path)) {
        throw new CliError('Refusing to delete the volume root. Delete the volume itself with volume delete.');
      }

      const { files, volumeName } = await filesFor(addresses.map(address => address.volume), options.profile);
      const label = addresses.map(address => shown(volumeName, address.path)).join(', ');

      // Only a person at a terminal can answer the prompt.
      if (!options.yes && !process.stdin.isTTY) {
        throw new CliError(`Refusing to delete ${label} without --yes.`);
      }

      await requireDeletion(ctx, label, Boolean(options.yes));

      for (const address of addresses) {
        try {
          await files.remove(address.path, Boolean(options.recursive));
        } catch (error) {
          if (error instanceof FileApiError && error.code === 'directory_not_empty') {
            throw new CliError(`${shown(volumeName, address.path)} is a folder that is not empty. Pass -r to delete it and everything in it.`);
          }

          throw error;
        }
      }
    });

  volume
    .command('cp')
    .argument('<paths...>', 'sources, then the destination: local or vol:// paths', { complete: volumePaths })
    .usage('[options] <sources...> <destination>')
    .description('Copy files or folders between this computer and a volume, or between volumes; interrupted copies resume when run again')
    .option('-r, --recursive', 'copy folders and everything in them')
    .option('--overwrite', 'replace files that already exist at the destination')
    .option('--update', 'skip files whose size and modification time already match')
    .option('--concurrency <n>', `files at a time, 1 to ${MAX_CONCURRENCY}`, '4')
    .option('--dry-run', 'show what would be copied')
    .option('-v, --verbose', 'print each file as it is copied')
    .option('--progress', 'show a running total on stderr')
    .option('--profile <name>', 'profile to use', { complete: profiles })
    .addHelpText('after', '\nExamples:\n  featherless-cloud volume cp -r ./weights vol://datasets/models/\n  featherless-cloud volume cp vol://datasets/models/out.bin .\n  featherless-cloud volume cp -r vol://datasets/models vol://checkpoints/')
    .action(async (paths: string[], options: {
      recursive?: boolean; overwrite?: boolean; update?: boolean; concurrency?: string; dryRun?: boolean; verbose?: boolean; progress?: boolean; profile?: string;
    }, cmd: Command) => {
      const ctx = createContext(cmd);
      const plan = direction(paths);
      const copy = { ...options, limit: concurrency(options.concurrency) };

      if (plan.kind === 'within') {
        const source = await filesFor(plan.sources.map(address => address.volume), options.profile, 'Name sources in one volume at a time.');
        finish(ctx, await between(ctx, { ...source, paths: plan.sources }, await filesFor([plan.destination.volume], options.profile), plan.destination, copy), Boolean(options.dryRun));
      } else if (plan.kind === 'upload') {
        finish(ctx, await upload(ctx, await filesFor([plan.destination.volume], options.profile), plan.sources, plan.destination, copy), Boolean(options.dryRun));
      } else {
        finish(ctx, await download(ctx, await filesFor(plan.sources.map(source => source.volume), options.profile), plan.sources, plan.destination, copy), Boolean(options.dryRun));
      }
    });

  volume
    .command('mv')
    .argument('<paths...>', 'sources, then the destination: local or vol:// paths', { complete: volumePaths })
    .usage('[options] <sources...> <destination>')
    .description('Move files or folders inside a volume, between volumes, or between this computer and a volume')
    .option('--overwrite', 'replace files at the destination')
    .option('--concurrency <n>', `files at a time, 1 to ${MAX_CONCURRENCY}`, '4')
    .option('-v, --verbose', 'print each file as it is moved')
    .option('--progress', 'show a running total on stderr')
    .option('--profile <name>', 'profile to use', { complete: profiles })
    .action(async (paths: string[], options: {
      overwrite?: boolean; concurrency?: string; verbose?: boolean; progress?: boolean; profile?: string;
    }, cmd: Command) => {
      const ctx = createContext(cmd);
      const plan = direction(paths);

      if (plan.kind !== 'upload' && plan.sources.some(source => !source.path)) {
        throw new CliError('Refusing to move a volume root. Name the files or folders in it.');
      }

      const copy = {
        overwrite: options.overwrite, limit: concurrency(options.concurrency), move: true, verbose: options.verbose, progress: options.progress,
      };

      if (plan.kind === 'within') {
        const from = await filesFor(plan.sources.map(address => address.volume), options.profile, 'Name sources in one volume at a time.');
        const into = await filesFor([plan.destination.volume], options.profile);

        // Each volume is separate storage: another volume gets a copy, then the source is deleted.
        if (into.files.volumeId !== from.files.volumeId) {
          finish(ctx, await between(ctx, { ...from, paths: plan.sources }, into, plan.destination, copy), false);
          return;
        }

        const { files, volumeName } = from;
        const existing = plan.destination.folder ? null : await files.entry(plan.destination.path);
        const intoFolder = plan.destination.folder || existing?.type === 'directory';
        requireFolder(intoFolder, plan.sources.length, shown(volumeName, plan.destination.path));
        const moved: FileEntry[] = [];

        for (const source of plan.sources) {
          const target = join(plan.destination.path, under(intoFolder, source.path.split('/').at(-1)!, ''));
          moved.push(await files.move(source.path, target, Boolean(options.overwrite)));
        }

        emit(ctx, moved, () => (options.verbose ? moved.map((item, index) => `${shown(volumeName, plan.sources[index]!.path)} -> ${shown(volumeName, item.path)}`) : []));
        return;
      }

      if (plan.kind === 'upload') {
        finish(ctx, await upload(ctx, await filesFor([plan.destination.volume], options.profile), plan.sources, plan.destination, copy), false);
      } else {
        finish(ctx, await download(ctx, await filesFor(plan.sources.map(source => source.volume), options.profile), plan.sources, plan.destination, copy), false);
      }
    });
}
