import { createHash } from 'node:crypto';
import {
  lstat, mkdir, opendir, readFile, realpath, rename, writeFile,
} from 'node:fs/promises';
import { dirname, join, resolve } from 'node:path';
import type { Command } from 'commander';
import { CliCommand } from '../../completions';
import { FileApiError, type FileEntry } from '../../clients/files';
import { getConfigDir } from '../../config';
import { createContext, type CliContext } from '../../context';
import { CliError, describeError } from '../../error';
import { profiles } from '../shared';
import {
  DEFAULT_CONCURRENCY, MAX_CONCURRENCY, concurrency, filesFor, formatSize, progressLine, remotePath, sameModifiedTime, volumeAddress, volumePaths,
  type VolumeFiles,
} from './files';

const MAX_DEPTH = 20;
const MAX_REPORT_ACTIONS = 1000;
/** This CLI's interrupted download staging files, `name.<12 hex>.partial`. */
const PARTIAL_PATTERN = /\.[0-9a-f]{12}\.partial$/;
/** Staging files in a volume: the CLI's, and the file server's unfinished uploads, `name.<8 hex>.partial`. */
const REMOTE_STAGING_PATTERN = /\.(?:[0-9a-f]{8}|[0-9a-f]{12})\.partial$/;

export type SyncMode = 'upload' | 'download' | 'both';

export interface SyncOptions {
  mode?: SyncMode;
  dryRun?: boolean;
  mkdirs?: boolean;
  force?: boolean;
  concurrency?: number;
  include?: string[];
  exclude?: string[];
  /** Print each file as it syncs. */
  verbose?: boolean;
  /** Show a running total on stderr. */
  progress?: boolean;
}

export type SyncAction = {
  path: string;
  action:
    | 'uploaded'
    | 'downloaded'
    | 'would-upload'
    | 'would-download'
    | 'skipped'
    | 'conflict'
    | 'created-directory'
    | 'would-create-directory';
  reason?: string;
};

export interface SyncReport {
  uploaded: number;
  uploadedBytes: number;
  downloaded: number;
  downloadedBytes: number;
  planned: number;
  skipped: number;
  conflicts: number;
  directories: number;
  failed: Array<{ path: string; error: string }>;
  actions: SyncAction[];
  truncated: boolean;
}

/** A file as the last sync left it on both sides: its size and whole-millisecond modification time. */
interface Synced {
  size: number;
  modifiedAt: number;
}

/** One planned step of a sync: a file one way or the other, or a folder to create. `key` is the path below the root. */
type Task =
  | { kind: 'upload'; key: string; local: string; remote: string; bytes: number; modifiedAt: number; replace: boolean; ifMatch?: string }
  | { kind: 'download'; key: string; local: string; entry: FileEntry; bytes: number; expected?: LocalFile }
  | { kind: 'mkdir-remote'; remote: string }
  | { kind: 'mkdir-local'; local: string };

/** How a folder pairs up below the sync root: on both sides, or only one. */
type WalkMode = 'pair' | 'local' | 'remote';

interface LocalFile {
  full: string;
  size: number;
  mtimeMs: number;
  dev: number;
  ino: number;
}

/**
 * A glob into a regular expression over a path below the sync root: `*` any run but `/`, `**` any
 * run, `?` one character but `/`. A pattern without a slash matches a name at any depth, as in
 * .gitignore; one with a slash matches from the root.
 */
export function glob(pattern: string, anchored = pattern.includes('/')): RegExp {
  if (!pattern || pattern.length > 256) {
    throw new CliError('Glob patterns must contain 1–256 characters.');
  }

  let text = anchored ? '^' : '^(?:.*/)?';

  for (let index = 0; index < pattern.length; index++) {
    const char = pattern[index]!;

    if (char === '*' && pattern[index + 1] === '*' && pattern[index + 2] === '/') {
      text += '(?:.*/)?';
      index += 2;
    } else if (char === '*' && pattern[index + 1] === '*') {
      text += '.*';
      index++;
    } else if (char === '*') {
      text += '[^/]*';
    } else if (char === '?') {
      text += '[^/]';
    } else {
      text += char.replace(/[|\\{}()[\]^$+?.]/g, '\\$&');
    }
  }

  return new RegExp(`${text}$`);
}

/**
 * Syncs a local folder with a folder in a volume, folder by folder.
 *
 * Each folder is listed on both sides and diffed locally — one bounded, paged
 * request per remote folder — so an interrupted sync resumes where the next
 * one lists, without walking the whole volume again. Sync remembers what it
 * left the same on both sides, so the side that changed since then wins
 * whatever the clocks say, a file changed on both sides is a conflict, and a
 * file deleted on one side stays deleted. For a file with no such memory the
 * newer modification time wins, and same-millisecond copies of different
 * sizes are a conflict rather than a guess. Sync never deletes: it only
 * sends, receives and creates folders. Every remote write is version-guarded,
 * so a file that changed remotely after the listing that planned its upload
 * fails with a conflict instead of replacing it.
 */
export async function syncVolumeFolder(ctx: CliContext, { files, volumeName }: VolumeFiles, local: string, remoteBase: string, options: SyncOptions = {}): Promise<SyncReport> {
  const mode = options.mode ?? 'both';

  if (!(['upload', 'download', 'both'] as string[]).includes(mode)) {
    throw new CliError('Sync mode must be upload, download or both.');
  }

  if (options.force && mode === 'both') {
    throw new CliError('--force needs --mode upload or --mode download; name which side wins.');
  }

  const limit = options.concurrency ?? DEFAULT_CONCURRENCY;

  if (!Number.isInteger(limit) || limit < 1 || limit > MAX_CONCURRENCY) {
    throw new CliError(`--concurrency must be 1 to ${MAX_CONCURRENCY}.`);
  }

  if (remoteBase) {
    remotePath(remoteBase);
  }

  const given = resolve(local);
  const rootStat = await lstat(given).catch(() => null);

  if (rootStat && (!rootStat.isDirectory() || rootStat.isSymbolicLink())) {
    throw new CliError(`${local} is not a real folder; sync the folder itself, not a link to it.`);
  }

  // Links above the folder are fine, like macOS's /tmp; its real path names it the same way every time.
  const root = rootStat ? await realpath(given) : given;

  if (!rootStat && (mode === 'upload' || !options.mkdirs)) {
    throw new CliError(`${local} does not exist; pass --mkdirs to create it for downloads.`);
  }

  if (!rootStat) {
    const parent = await lstat(dirname(root)).catch(() => null);

    if (!parent?.isDirectory() || dirname(root) === root) {
      throw new CliError(`The parent of ${local} does not exist; sync creates only the folder itself.`);
    }
  }

  const remoteRoot = remoteBase ? await files.entry(remoteBase) : null;
  const missingRemote = Boolean(remoteBase) && !remoteRoot;

  if (remoteRoot?.type === 'file') {
    throw new CliError(`vol://${volumeName}/${remoteBase} is a file, not a folder.`);
  }

  if (missingRemote && (mode === 'download' || !options.mkdirs)) {
    throw new CliError(`vol://${volumeName}/${remoteBase} does not exist; pass --mkdirs to create it for uploads.`);
  }

  // What the last sync of this pair of folders left the same on both sides, by path below the
  // root. A side missing whole is a fresh copy, not a deletion of everything, so it starts over.
  const pair = createHash('sha256').update([files.volumeId, remoteBase, root].join('\n')).digest('hex');
  const historyFile = join(getConfigDir(), 'featherless-cloud-sync', `${pair.slice(0, 32)}.json`);
  let saved: Record<string, Synced> = {};

  if (rootStat && !missingRemote) {
    const text = await readFile(historyFile, 'utf8').catch(() => null);

    try {
      saved = text ? (JSON.parse(text) as { files: Record<string, Synced> }).files : {};
    } catch {
      saved = {};
    }
  }

  const history = new Map(Object.entries(saved));
  // What this run leaves the same on both sides, saved over the history when it ends.
  const synced = new Map(history);
  const historyFolders = new Set<string>();
  const historyChildren = new Map<string, string[]>();

  for (const key of history.keys()) {
    const slash = key.lastIndexOf('/');
    const parent = slash === -1 ? '' : key.slice(0, slash);
    const siblings = historyChildren.get(parent);

    if (siblings) {
      siblings.push(key);
    } else {
      historyChildren.set(parent, [key]);
    }

    for (let at = key.indexOf('/'); at !== -1; at = key.indexOf('/', at + 1)) {
      historyFolders.add(key.slice(0, at));
    }
  }

  const report: SyncReport = {
    uploaded: 0,
    uploadedBytes: 0,
    downloaded: 0,
    downloadedBytes: 0,
    planned: 0,
    skipped: 0,
    conflicts: 0,
    directories: 0,
    failed: [],
    actions: [],
    truncated: false,
  };

  const record = (action: SyncAction): void => {
    if (report.actions.length < MAX_REPORT_ACTIONS) {
      report.actions.push(action);
    } else {
      report.truncated = true;
    }
  };

  const json = ctx.mode === 'json';
  const total = { files: 0, bytes: 0 };
  const moved = new Map<Task, number>();
  const started = Date.now();
  const terminal = Boolean(process.stderr.isTTY);
  const live = Boolean(options.progress) && !json;
  let doneFiles = 0;
  let shownAt = 0;
  let drawn = false;

  const show = (force = false): void => {
    if (!live || total.files === 0 || (!force && Date.now() - shownAt < (terminal ? 250 : 5_000))) {
      return;
    }

    shownAt = Date.now();
    const done = { files: doneFiles, bytes: [...moved.values()].reduce((sum, bytes) => sum + bytes, 0) };
    const line = progressLine('Syncing', done, total, Date.now() - started);

    if (terminal) {
      process.stderr.write(`\r\x1b[K${line}`);
      drawn = true;
    } else {
      process.stderr.write(`${line}\n`);
    }
  };

  const say = (stream: NodeJS.WriteStream, line: string): void => {
    if (json) {
      return;
    }

    if (drawn) {
      process.stderr.write('\r\x1b[K');
      drawn = false;
    }

    stream.write(`${line}\n`);
  };

  const conflict = (path: string, reason: string): void => {
    report.conflicts += 1;
    record({ path, action: 'conflict', reason });
    say(process.stderr, `conflict ${path}: ${reason}`);
  };

  const target = (relative: string): string => [remoteBase, relative].filter(Boolean).join('/');
  const tooDeep = (relative: string): boolean => target(relative).split('/').length > MAX_DEPTH;
  const include = (options.include ?? []).map(pattern => glob(pattern));
  const exclude = (options.exclude ?? []).map(pattern => glob(pattern));

  const accepted = (relative: string): boolean => (!include.length || include.some(regex => regex.test(relative)))
    && !exclude.some(regex => regex.test(relative));

  // A folder is skipped whole when a pattern names the folder itself:
  // `node_modules/**` covers everything below node_modules, and a last part
  // without wildcards — `node_modules`, `**/node_modules` — names the folder.
  // `logs/*` and `*.log` name files, so nested files below a folder they
  // match must still be reachable.
  const literal = (pattern: string): boolean => !/[*?]/.test(pattern);

  const folderExcludes = (options.exclude ?? []).flatMap(pattern => {
    if (pattern.endsWith('/**')) {
      return [glob(pattern.slice(0, -3) || '*', true)];
    }

    return literal(pattern.slice(pattern.lastIndexOf('/') + 1)) ? [glob(pattern)] : [];
  });

  const excluded = (relative: string): boolean => folderExcludes.some(regex => regex.test(relative));

  // The other side of the globs: with --include, a folder nothing below can
  // match is skipped too. A pattern can only reach below a folder when it
  // matches at any depth, or its literal prefix can be a prefix of the paths
  // below, or starts with a wildcard; anything else would need a path the
  // glob cannot spell.
  const literalPrefix = (pattern: string): string => {
    const wildcard = /[*?]/.exec(pattern);
    return wildcard ? pattern.slice(0, wildcard.index) : pattern;
  };

  const reachable = (folder: string): boolean => !(options.include ?? []).some(Boolean) || (options.include ?? []).some(pattern => {
    const prefix = literalPrefix(pattern);
    return !pattern.includes('/') || prefix === '' || `${folder}/`.startsWith(prefix) || prefix.startsWith(`${folder}/`);
  });

  /** The local side of one folder: files to diff, folders to walk, and names nothing can replace. */
  async function listLocal(folder: string, relative: string): Promise<{ files: Map<string, LocalFile>; dirs: Set<string>; special: Set<string> }> {
    const files = new Map<string, LocalFile>();
    const dirs = new Set<string>();
    const special = new Set<string>();

    for await (const child of await opendir(folder)) {
      // Children are keyed by their path below the sync root, the same shape
      // the remote side is keyed by, so the two sides diff below the root.
      const name = relative ? `${relative}/${child.name}` : child.name;

      if (child.isSymbolicLink()) {
        special.add(name);
        continue;
      }

      const full = join(folder, child.name);
      const stats = await lstat(full);

      if (stats.isDirectory()) {
        dirs.add(name);
      } else if (stats.isFile()) {
        // An interrupted download's staging file is not a file to synchronize.
        if (PARTIAL_PATTERN.test(child.name) || child.name === '.featherless-uploads') {
          continue;
        }

        files.set(name, {
          full, size: stats.size, mtimeMs: stats.mtimeMs, dev: stats.dev, ino: stats.ino,
        });
      } else {
        special.add(name);
      }
    }

    return { files, dirs, special };
  }

  /** The remote side of one folder, or nothing when the folder is not there. */
  async function listRemote(folder: string): Promise<FileEntry[] | null> {
    try {
      return await files.list(folder);
    } catch (error) {
      if (error instanceof FileApiError && error.status === 404) {
        return null;
      }

      throw error;
    }
  }

  // Listings run ahead of the walk, a few at once, so it doesn't wait a round trip per folder. Six
  // at prod's ~0.2 s round trip stays under the file server's 32 listings a second.
  const LISTINGS_AHEAD = 6;

  interface Listing {
    local: Awaited<ReturnType<typeof listLocal>> | null;
    remote: FileEntry[];
  }

  const listings = new Map<string, Promise<Listing>>();
  const listingWaiters: Array<() => void> = [];
  let listingsRunning = 0;

  async function listFolder(relative: string, walkMode: WalkMode): Promise<Listing> {
    if (listingsRunning < LISTINGS_AHEAD) {
      listingsRunning += 1;
    } else {
      // The listing that finishes hands its place straight to this one.
      await new Promise<void>(resolve => listingWaiters.push(resolve));
    }

    try {
      const [local, remote] = await Promise.all([
        walkMode === 'remote' ? null : listLocal(join(root, relative), relative),
        walkMode === 'local' ? [] : listRemote(target(relative)).then(entries => entries ?? []),
      ]);

      return { local, remote };
    } finally {
      const next = listingWaiters.shift();

      if (next) {
        next();
      } else {
        listingsRunning -= 1;
      }
    }
  }

  /** Starts listing a folder the walk will reach, where walking it lists it at all. */
  function listAhead(relative: string, walkMode: WalkMode): void {
    const skipped = walkMode !== 'pair' && historyFolders.has(relative) && !options.force;
    const refused = (walkMode === 'local' && (tooDeep(relative) || !options.mkdirs)) || (walkMode === 'remote' && !options.mkdirs);

    if (listings.has(relative) || skipped || refused) {
      return;
    }

    const listing = listFolder(relative, walkMode);
    // The walk reads the failure when it reaches the folder; until then it is not unhandled.
    listing.catch(() => undefined);
    listings.set(relative, listing);
  }

  /** Walks one folder on whichever sides hold it, yielding the tasks it plans. */
  async function* walk(relative: string, walkMode: WalkMode): AsyncGenerator<Task> {
    // A folder the last sync left on both sides and one side has deleted since stays deleted.
    if (walkMode !== 'pair' && historyFolders.has(relative) && !options.force) {
      report.skipped += 1;

      record({
        path: target(relative),
        action: 'skipped',
        reason: walkMode === 'remote' ? 'Deleted on this computer since the last sync.' : 'Deleted in the volume since the last sync.',
      });

      return;
    }

    if (walkMode === 'local') {
      if (tooDeep(relative)) {
        conflict(target(relative), 'The volume cannot hold a path this deep.');
        return;
      }

      if (!options.mkdirs) {
        conflict(target(relative), 'The remote folder is missing; pass --mkdirs to create it.');
        return;
      }

      yield { kind: 'mkdir-remote', remote: target(relative) };
    } else if (walkMode === 'remote') {
      if (!options.mkdirs) {
        conflict(target(relative), 'The local folder is missing; pass --mkdirs to create it.');
        return;
      }

      yield { kind: 'mkdir-local', local: join(root, relative) };
    }

    const listing = listings.get(relative) ?? listFolder(relative, walkMode);
    listings.delete(relative);
    const { local, remote } = await listing;

    // Both sides are keyed by the path below the sync root, so they pair up
    // at every depth, not just at the root itself.
    const key = (name: string): string => (relative ? `${relative}/${name}` : name);
    const remoteDirs = new Set(remote.filter(entry => entry.type === 'directory').map(entry => key(entry.name)));
    const remoteFiles = new Map(remote.filter(entry => entry.type === 'file').map(entry => [key(entry.name), entry] as const));
    const special = local?.special ?? new Set<string>();
    const names = [...new Set([...(local?.files.keys() ?? []), ...(local?.dirs ?? []), ...special, ...remoteDirs, ...remoteFiles.keys()])].sort();
    const present = new Set(names);

    // A file both sides have dropped since the last sync is forgotten.
    for (const known of historyChildren.get(relative) ?? []) {
      if (!present.has(known)) {
        synced.delete(known);
      }
    }

    // Whether one side's copy is still the one the last sync left on both sides.
    const unchanged = (before: Synced, size: number | undefined, modifiedAt: number): boolean => size === before.size && modifiedAt === before.modifiedAt;

    // The folders the loop below descends into, in its order, as the loop decides them.
    const childMode = (name: string): WalkMode | null => {
      const isDir = local?.dirs.has(name) ?? false;

      if (special.has(name) || ((isDir || remoteDirs.has(name)) && (excluded(name) || !reachable(name)))) {
        return null;
      }

      if (isDir && remoteDirs.has(name)) {
        return 'pair';
      }

      if (!isDir && !local?.files.has(name) && remoteDirs.has(name)) {
        return mode === 'upload' ? null : 'remote';
      }

      if (isDir && !remoteFiles.has(name) && mode !== 'download') {
        return 'local';
      }

      return null;
    };

    const children = names.flatMap(name => {
      const childWalk = childMode(name);
      return childWalk ? [[name, childWalk] as const] : [];
    });

    let ahead = 0;
    let entered = 0;

    // Before entering a child folder, keep it and the next few listing in the background.
    const enterChild = (): void => {
      while (ahead < children.length && ahead <= entered + LISTINGS_AHEAD) {
        listAhead(...children[ahead]!);
        ahead += 1;
      }

      entered += 1;
    };

    for (const name of names) {
      const file = local?.files.get(name);
      const isDir = local?.dirs.has(name) ?? false;
      const entry = remoteFiles.get(name);

      // A folder or a special name the globs skip is passed over whole, its
      // files with it; files themselves the diff below decides.
      if ((isDir || remoteDirs.has(name) || special.has(name)) && (excluded(name) || !reachable(name))) {
        continue;
      }

      // A local symlink or special file is never sent, and nothing remote
      // may take its name either.
      if (special.has(name)) {
        if (remoteDirs.has(name) || entry) {
          conflict(target(name), 'A remote file or folder has this local symlink or special file name.');
        } else {
          report.skipped += 1;
          record({ path: target(name), action: 'skipped', reason: 'A local symlink or special file is not synchronized.' });
        }

        continue;
      }

      // Folders on both sides.
      if (isDir && remoteDirs.has(name)) {
        enterChild();
        yield* walk(name, 'pair');
        continue;
      }

      // A name used as a folder on one side and a file on the other.
      if (isDir && entry) {
        conflict(target(name), 'A remote file has this folder name.');
        continue;
      }

      if (!isDir && file && remoteDirs.has(name)) {
        conflict(target(name), 'A remote folder has this file name.');
        continue;
      }

      // A folder only the remote holds: nothing local to send under it.
      if (!isDir && !file && remoteDirs.has(name)) {
        if (mode !== 'upload') {
          enterChild();
          yield* walk(name, 'remote');
        }

        continue;
      }

      // A folder only the local side holds.
      if (isDir && !entry && mode !== 'download') {
        enterChild();
        yield* walk(name, 'local');
        continue;
      }

      // Files: on one side, the other, or both.
      if (file && entry) {
        if (!accepted(name)) {
          continue;
        }

        const remoteAt = Date.parse(entry.modifiedAt);
        const localAt = Math.floor(file.mtimeMs);
        let direction: 'upload' | 'download';

        // Identical files have nothing to send either way, even with --force.
        if (sameModifiedTime(file.mtimeMs, entry.modifiedAt) && file.size === entry.sizeBytes) {
          synced.set(name, { size: file.size, modifiedAt: remoteAt });
          report.skipped += 1;
          record({ path: target(name), action: 'skipped' });
          continue;
        }

        // Which sides changed since the last sync left them the same, when it did.
        const before = history.get(name);
        const localChanged = before !== undefined && !unchanged(before, file.size, localAt);
        const remoteChanged = before !== undefined && !unchanged(before, entry.sizeBytes, remoteAt);

        if (options.force) {
          direction = mode as 'upload' | 'download';
        } else if (!Number.isFinite(remoteAt)) {
          conflict(target(name), 'The remote file has no timestamp; pass --force to choose a side.');
          continue;
        } else if (localChanged && remoteChanged) {
          conflict(target(name), 'Both sides changed since the last sync; pass --force to choose one.');
          continue;
        } else if (localChanged !== remoteChanged) {
          // The side that changed since the last sync wins, whatever the clocks say.
          direction = localChanged ? 'upload' : 'download';
        } else if (localAt === remoteAt) {
          conflict(target(name), 'Both sides changed at the same time with different sizes; pass --force to choose one.');
          continue;
        } else {
          direction = localAt > remoteAt ? 'upload' : 'download';
        }

        if (!options.force && mode !== 'both' && direction !== mode) {
          if (file.size !== entry.sizeBytes) {
            conflict(target(name), `The ${direction === 'upload' ? 'local' : 'remote'} copy has the newer change, with a different size; pass --force to replace it.`);
          } else {
            report.skipped += 1;
            record({ path: target(name), action: 'skipped' });
          }

          continue;
        }

        if (direction === 'upload') {
          if (tooDeep(name)) {
            conflict(target(name), 'The volume cannot hold a path this deep.');
          } else if (!entry.version) {
            conflict(target(name), 'The remote file has no version to guard its replacement.');
          } else {
            yield {
              kind: 'upload', key: name, local: file.full, remote: target(name), bytes: file.size, modifiedAt: localAt, replace: true, ifMatch: entry.version,
            };
          }
        } else {
          yield {
            kind: 'download', key: name, local: join(root, name), entry, bytes: entry.sizeBytes ?? 0, expected: file,
          };
        }
      } else if (file) {
        if (mode === 'download' || !accepted(name)) {
          continue;
        }

        const before = history.get(name);

        // Deleted in the volume since the last sync: it stays deleted, unless it changed here since.
        if (before && !options.force) {
          if (unchanged(before, file.size, Math.floor(file.mtimeMs))) {
            report.skipped += 1;
            record({ path: target(name), action: 'skipped', reason: 'Deleted in the volume since the last sync.' });
          } else {
            conflict(target(name), 'Deleted in the volume and changed here since the last sync; pass --force to send it again.');
          }

          continue;
        }

        if (tooDeep(name)) {
          conflict(target(name), 'The volume cannot hold a path this deep.');
        } else {
          yield {
            kind: 'upload', key: name, local: file.full, remote: target(name), bytes: file.size, modifiedAt: Math.floor(file.mtimeMs), replace: false,
          };
        }
      } else if (entry) {
        // A staging file — this CLI's, or an upload still in progress — is never a file to keep.
        if (REMOTE_STAGING_PATTERN.test(entry.name) || entry.name === '.featherless-uploads') {
          report.skipped += 1;
          record({ path: target(name), action: 'skipped', reason: 'A staging file of an unfinished transfer is not synchronized.' });
          continue;
        }

        if (mode === 'upload' || !accepted(name)) {
          continue;
        }

        const before = history.get(name);

        // Deleted on this computer since the last sync: it stays deleted, unless it changed in the volume since.
        if (before && !options.force) {
          if (unchanged(before, entry.sizeBytes, Date.parse(entry.modifiedAt))) {
            report.skipped += 1;
            record({ path: target(name), action: 'skipped', reason: 'Deleted on this computer since the last sync.' });
          } else {
            conflict(target(name), 'Deleted on this computer and changed in the volume since the last sync; pass --force to fetch it again.');
          }

          continue;
        }

        yield {
          kind: 'download', key: name, local: join(root, name), entry, bytes: entry.sizeBytes ?? 0,
        };
      }
    }
  }

  /** Runs one planned task, recording what it did or why it could not. */
  async function run(task: Task): Promise<boolean> {
    if (task.kind === 'mkdir-remote') {
      if (options.dryRun) {
        report.directories += 1;
        record({ path: task.remote, action: 'would-create-directory' });
        say(process.stdout, `would create folder vol://${volumeName}/${task.remote}`);
        return true;
      }

      try {
        await files.mkdir(task.remote, true);
      } catch (error) {
        report.failed.push({ path: task.remote, error: describeError(error) });
        say(process.stderr, `vol://${volumeName}/${task.remote}: ${describeError(error)}`);
        return false;
      }

      report.directories += 1;
      record({ path: task.remote, action: 'created-directory' });

      if (options.verbose) {
        say(process.stdout, `created folder vol://${volumeName}/${task.remote}`);
      }

      return true;
    }

    if (task.kind === 'mkdir-local') {
      if (options.dryRun) {
        report.directories += 1;
        record({ path: task.local, action: 'would-create-directory' });
        say(process.stdout, `would create folder ${task.local}`);
        return true;
      }

      try {
        await mkdir(task.local, { recursive: true });
      } catch (error) {
        report.failed.push({ path: task.local, error: describeError(error) });
        say(process.stderr, `${task.local}: ${describeError(error)}`);
        return false;
      }

      report.directories += 1;
      record({ path: task.local, action: 'created-directory' });

      if (options.verbose) {
        say(process.stdout, `created folder ${task.local}`);
      }

      return true;
    }

    if (task.kind === 'upload') {
      if (options.dryRun) {
        report.planned += 1;
        record({ path: task.remote, action: 'would-upload' });
        say(process.stdout, `would upload ${task.local} -> vol://${volumeName}/${task.remote}`);
        return true;
      }

      try {
        await files.upload(task.local, task.remote, {
          mode: task.replace ? 'overwrite' : 'create',
          parents: true,
          ifMatch: task.ifMatch,
          onProgress: (sent: number) => moved.set(task, sent),
        });
      } catch (error) {
        if (error instanceof FileApiError && error.status === 412) {
          conflict(task.remote, error.code === 'already_exists'
            ? 'A file appeared while syncing.'
            : 'The remote file changed while syncing.');

          return false;
        }

        report.failed.push({ path: task.remote, error: describeError(error) });
        say(process.stderr, `vol://${volumeName}/${task.remote}: ${describeError(error)}`);
        return false;
      }

      synced.set(task.key, { size: task.bytes, modifiedAt: task.modifiedAt });
      report.uploaded += 1;
      report.uploadedBytes += task.bytes;
      record({ path: task.remote, action: 'uploaded' });

      if (options.verbose) {
        say(process.stdout, `${task.local} -> vol://${volumeName}/${task.remote}`);
      }

      return true;
    }

    if (options.dryRun) {
      report.planned += 1;
      record({ path: task.entry.path, action: 'would-download' });
      say(process.stdout, `would download vol://${volumeName}/${task.entry.path} -> ${task.local}`);
      return true;
    }

    // The listing that planned this download read the local side as it was;
    // refuse to replace anything that is there now, whatever the plan said.
    if (task.expected) {
      const now = await lstat(task.local).catch(() => null);

      if (!now || now.dev !== task.expected.dev || now.ino !== task.expected.ino
        || now.size !== task.expected.size || now.mtimeMs !== task.expected.mtimeMs) {
        conflict(task.entry.path, 'The local file changed while syncing.');
        return false;
      }
    } else {
      const appeared = await lstat(task.local).catch(() => null);

      if (appeared) {
        conflict(task.entry.path, 'A local file or folder appeared while syncing.');
        return false;
      }
    }

    try {
      await files.download(task.entry, task.local, (received: number) => moved.set(task, received));
    } catch (error) {
      report.failed.push({ path: task.entry.path, error: describeError(error) });
      say(process.stderr, `vol://${volumeName}/${task.entry.path}: ${describeError(error)}`);
      return false;
    }

    synced.set(task.key, { size: task.bytes, modifiedAt: Date.parse(task.entry.modifiedAt) });
    report.downloaded += 1;
    report.downloadedBytes += task.bytes;
    record({ path: task.entry.path, action: 'downloaded' });

    if (options.verbose) {
      say(process.stdout, `vol://${volumeName}/${task.entry.path} -> ${task.local}`);
    }

    return true;
  }

  // The walk plans folder by folder while the workers transfer, so a deep tree
  // starts moving before the whole volume has been listed. A worker that hits
  // a walk failure lets the rest drain, then the failure surfaces.
  let rootMode: WalkMode = 'pair';

  if (!rootStat) {
    rootMode = 'remote';
  } else if (missingRemote) {
    rootMode = 'local';
  }

  const queue = walk('', rootMode);
  let planFailure: unknown = null;
  let planFailed = false;
  let declared = false;

  const pull = async (): Promise<void> => {
    for (;;) {
      let next: IteratorResult<Task>;

      try {
        next = await queue.next();
      } catch (error) {
        planFailure = error;
        planFailed = true;
        return;
      }

      if (next.done) {
        return;
      }

      const task = next.value;

      if (task.kind === 'upload' || task.kind === 'download') {
        total.files += 1;
        total.bytes += task.bytes;

        if (!declared) {
          declared = true;
          show(true);
        } else {
          show();
        }
      }

      const ok = await run(task);

      if (task.kind === 'upload' || task.kind === 'download') {
        doneFiles += 1;

        // A finished transfer counts whole; a failed one keeps whatever
        // progress its last report showed.
        if (ok && !options.dryRun) {
          moved.set(task, task.bytes);
        }
      }

      show();
    }
  };

  await Promise.all(Array.from({ length: limit }, pull));

  if (drawn) {
    process.stderr.write('\n');
  }

  // Kept even when the walk failed: what did finish stays remembered for the next run.
  if (!options.dryRun) {
    const draft = `${historyFile}.${process.pid}.tmp`;
    await mkdir(dirname(historyFile), { recursive: true });
    await writeFile(draft, JSON.stringify({ files: Object.fromEntries(synced) }));
    await rename(draft, historyFile);
  }

  if (planFailed) {
    throw planFailure;
  }

  return report;
}

function emit(ctx: CliContext, report: SyncReport, lines: () => string[]): void {
  if (ctx.mode === 'json') {
    console.log(JSON.stringify(report, null, 2));
    return;
  }

  for (const line of lines()) {
    process.stdout.write(`${line}\n`);
  }
}

const collect = (pattern: string, values: string[]): string[] => [...values, pattern];

export const volumeSyncCommand = new CliCommand('sync')
  .argument('<local-folder>', 'folder on this computer')
  .argument('<remote-folder>', 'vol://<volume>/<path> folder in the volume', { complete: volumePaths })
  .usage('[options] <local-folder> <vol://<volume>/<path>')
  .description('Sync a folder with a folder in a volume, sending what changed each way; never deletes')
  .option('--mode <mode>', 'upload, download or both', 'both')
  .option('--dry-run', 'show what would be sent, received and created')
  .option('--mkdirs', 'create missing folders on either side')
  .option('--force', 'send even when the chosen side is older (needs --mode upload or --mode download)')
  .option('--concurrency <n>', `files at a time, 1 to ${MAX_CONCURRENCY}`, String(DEFAULT_CONCURRENCY))
  .option('--include <glob>', 'only relative paths matching a glob (*, ** and ?)', collect, [] as string[])
  .option('--exclude <glob>', 'skip relative paths matching a glob (*, ** and ?)', collect, [] as string[])
  .option('-v, --verbose', 'print each file as it syncs')
  .option('--progress', 'show a running total on stderr')
  .option('--profile <name>', 'profile to use', { complete: profiles })
  .addHelpText('after', '\nExamples:\n  featherless-cloud volume sync ./weights vol://datasets/models/\n  featherless-cloud volume sync --mode download --mkdirs . vol://datasets/models\n  featherless-cloud volume sync --dry-run ./ckpt vol://datasets/ckpt')
  .action(async (local: string, remote: string, options: {
    mode?: string; dryRun?: boolean; mkdirs?: boolean; force?: boolean; concurrency?: string;
    include?: string[]; exclude?: string[]; verbose?: boolean; progress?: boolean; profile?: string;
  }, cmd: Command) => {
    const ctx = createContext(cmd);
    const address = volumeAddress(remote);

    const data = await filesFor([address.volume], options.profile);

    const report = await syncVolumeFolder(ctx, data, local, address.path, {
      ...options,
      mode: options.mode as SyncMode | undefined,
      concurrency: concurrency(options.concurrency),
    });

    emit(ctx, report, () => [
      options.dryRun
        ? `Would upload and download ${report.planned} file(s), create ${report.directories} folder(s); skipped ${report.skipped}, conflict(s) ${report.conflicts}.`
        : `Uploaded ${report.uploaded} file(s), ${formatSize(report.uploadedBytes)}; downloaded ${report.downloaded} file(s), ${formatSize(report.downloadedBytes)}; created ${report.directories} folder(s); skipped ${report.skipped}, conflict(s) ${report.conflicts}, failed ${report.failed.length}.`,
      ...(report.truncated ? ['… and more actions not listed.'] : []),
    ]);

    if (report.failed.length || report.conflicts) {
      process.exitCode = 1;
    }
  });
