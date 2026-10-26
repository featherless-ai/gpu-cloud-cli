import { execFileSync } from 'node:child_process';
import { mkdtempSync, readFileSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { Command } from 'commander';
import {
  beforeEach, describe, expect, it, vi,
} from 'vitest';
import {
  bashScript,
  CliCommand,
  commandTree,
  completeWord,
  profileFromCommand,
  tokenizeLine,
  walkCommandTree,
  zshScript,
  type Completer,
  type CompleterContext,
  type CompletionNode,
} from '../src/completions';
import {
  cpuCores, gpuCounts, gpuTypes, instances, jobs, profiles, registries, templates, volumeMounts, volumes,
} from '../src/commands/shared';
import { volumePaths } from '../src/commands/volume/files';
import type { CloudClient } from '../src/clients/cloud';
import { FilesClient, type FileEntry } from '../src/clients/files';
import { loadConfig } from '../src/config';
import { completeCommand, completionCommand } from '../src/commands/completion';
import { execCommand } from '../src/commands/exec';
import { instanceCommand } from '../src/commands/instance';
import { jobCommand } from '../src/commands/job';
import { loginCommand } from '../src/commands/login';
import { pricingCommand } from '../src/commands/pricing';
import { volumeCommand } from '../src/commands/volume';

vi.mock('../src/clients/files', async importOriginal => ({ ...await importOriginal<typeof import('../src/clients/files')>(), FilesClient: vi.fn() }));
vi.mock('../src/config', async importOriginal => ({ ...await importOriginal<typeof import('../src/config')>(), loadConfig: vi.fn() }));

const DATASETS = { id: '11111111-2222-4333-8444-555555555555', name: 'datasets' };
const MODELS = { id: '22222222-2222-4333-8444-555555555555', name: 'models' };
const MY_DATA = { id: '33333333-2222-4333-8444-555555555555', name: 'my data' };
const TMUX = { id: '44444444-2222-4333-8444-555555555555', shortId: 'tmxbnd1234', name: 'tmux-bundle-ubuntu' };
const DATASET_INSTANCE = { id: '55555555-2222-4333-8444-555555555555', shortId: 'dtspr0d012', name: 'datasets-prod' };

const dir = (name: string, path: string): FileEntry => ({ name, path, type: 'directory', modifiedAt: '2026-01-01T00:00:00Z' });

const file = (name: string, path: string): FileEntry => ({
  name, path, type: 'file', sizeBytes: 10, modifiedAt: '2026-01-01T00:00:00Z',
});

/** Completer context whose client is the given fake. */
function ctx(cloud: object = {}, args: string[] = []): CompleterContext {
  return { args, cloud: async () => cloud as unknown as CloudClient };
}

/** A client that lists `list` as the account's volumes. */
const withVolumes = (list: Array<{ id: string; name: string }>) => ({ listVolumes: async () => list });

/** Folder listings answer from `list`, called with the volume id and folder. */
function listing(list: (volumeId: string, folder: string) => Promise<FileEntry[]>): void {
  vi.mocked(FilesClient).mockImplementation(function (_cloud: unknown, volumeId: string) {
    return { list: (folder: string) => list(volumeId, folder) } as unknown as FilesClient;
  });
}

const entries = (items: FileEntry[]) => listing(async () => items);

beforeEach(() => {
  entries([]);
});

function testTree(): CompletionNode {
  const program = new CliCommand('featherless-cloud')
    .option('--json', 'output as JSON')
    .option('--profile <name>', 'profile to use', { complete: profiles });

  const volume = program.command('volume');

  volume.command('ls').argument('[path]', 'folder', { complete: volumePaths }).option('--profile <name>', 'profile', { complete: profiles });
  volume.command('rm').argument('<paths...>', 'paths', { complete: volumePaths }).option('--yes', 'skip the confirmation prompt');
  program.command('exec').option('--name <name>', 'Run Job name');
  program.command('get <id>');
  program.command('completion').argument('<shell>', 'shell', { choices: ['bash', 'zsh'] });

  return commandTree(program);
}

describe('CliCommand', () => {
  const quiet = (command: CliCommand) => command.exitOverride().configureOutput({ writeErr: () => {} });

  it('keeps Commander option forms', () => {
    const make = () => quiet(new CliCommand('x'))
      .option('--tail <n>', 'lines', '500')
      .option('--env <K=V>', 'env', (value: string, previous: string[]) => [...previous, value], [])
      .option('--tag <tags...>', 'tags', ['a']);

    expect(make().parse([], { from: 'user' }).opts()).toEqual({ tail: '500', env: [], tag: ['a'] });
    expect(make().parse(['--env', 'A=1', '--env', 'B=2'], { from: 'user' }).opts().env).toEqual(['A=1', 'B=2']);
  });

  it('applies defaultValue, parse and choices from a config', () => {
    const make = () => quiet(new CliCommand('x'))
      .argument('[path]', 'path', { defaultValue: '.', complete: volumePaths })
      .option('--volume <name:path>', 'mount', { parse: (value: string, previous: string[]) => [...previous, value], defaultValue: [], complete: volumeMounts })
      .option('--compute-type <type>', 'type', { choices: ['gpu', 'cpu'], defaultValue: 'gpu' })
      .action(() => {});

    const defaults = make().parse([], { from: 'user' });

    expect(defaults.opts()).toEqual({ volume: [], computeType: 'gpu' });
    expect(defaults.processedArgs).toEqual(['.']);
    expect(make().parse(['--volume', 'a:/x', '--volume', 'b:/y'], { from: 'user' }).opts().volume).toEqual(['a:/x', 'b:/y']);
    expect(() => make().parse(['--compute-type', 'tpu'], { from: 'user' })).toThrow(/Allowed choices are gpu, cpu/);
  });

  it('registers each completer on the option or argument it defines', () => {
    const program = new CliCommand('x')
      .argument('[path]', 'path', { complete: volumePaths })
      .option('--volume <name:path>', 'mount', { complete: volumeMounts })
      .option('--tail <n>', 'lines', '500');

    const tree = commandTree(program);

    expect(tree.arguments[0]?.complete).toBe(volumePaths);
    expect(tree.options.find(option => option.flags.startsWith('--volume'))?.complete).toBe(volumeMounts);
    expect(tree.options.find(option => option.flags.startsWith('--tail'))?.complete).toBeUndefined();
  });

  it('creates CliCommand subcommands', () => {
    expect(new CliCommand('x').command('volume').command('ls')).toBeInstanceOf(CliCommand);
  });
});

describe('commandTree', () => {
  it('includes visible commands with aliases and global options, excludes hidden ones', () => {
    const program = new Command('featherless-cloud').option('--json', 'json output');

    program.command('runjob').alias('job').command('list');
    program.command('_check-update', { hidden: true });

    const tree = commandTree(program);

    expect(tree.commands.map(command => command.name)).toEqual(['runjob', 'job', 'help']);
    expect(tree.commands[0]?.commands.map(command => command.name)).toEqual(['list', 'help']);

    expect(tree.options).toEqual([
      { flags: '--json', required: false },
      { flags: '--help', required: false },
    ]);
  });

  it('gives help nodes the parent command to list', () => {
    const help = testTree().commands.find(command => command.name === 'help')!;

    expect(help.helpFor?.commands.map(command => command.name)).toContain('volume');
  });
});

describe('tokenizeLine', () => {
  it('decodes escapes and quotes', () => {
    expect(tokenizeLine('featherless-cloud volume ls vol://my\\ data/x')).toEqual({
      words: ['featherless-cloud', 'volume', 'ls', 'vol://my data/x'],
      open: true,
    });

    expect(tokenizeLine('featherless-cloud volume ls "vol://my da')).toEqual({
      words: ['featherless-cloud', 'volume', 'ls', 'vol://my da'],
      open: true,
    });

    expect(tokenizeLine('featherless-cloud volume ls ')).toEqual({
      words: ['featherless-cloud', 'volume', 'ls'],
      open: false,
    });
  });
});

describe('walkCommandTree', () => {
  it('resolves the deepest command, options, and positional values', () => {
    const tree = testTree();

    expect(walkCommandTree(tree, ['volume', 'ls']).node.name).toBe('ls');
    expect(walkCommandTree(tree, ['volume', 'ls', '--profile', 'prod']).afterFlag).toBeNull();
    expect(walkCommandTree(tree, ['volume', 'ls', '--profile']).afterFlag?.flags).toBe('--profile <name>');
    expect(walkCommandTree(tree, ['volume', 'rm', 'vol://x/', '--yes', 'vol://y/']).args).toEqual(['vol://x/', 'vol://y/']);
  });

  it('walks aliases', () => {
    const program = new Command('featherless-cloud');

    program.command('runjob').alias('job').command('list');

    expect(walkCommandTree(commandTree(program), ['job', 'list']).node.name).toBe('list');
  });
});

describe('completeWord', () => {
  const complete = (word: string, words: string[], context = ctx()) => completeWord(word, walkCommandTree(testTree(), words), context);

  it('completes subcommands after the program name', async () => {
    await expect(complete('vol', [])).resolves.toEqual(['volume']);
    await expect(complete('vo', [])).resolves.toEqual(['volume']);
  });

  it('completes flags at any position', async () => {
    await expect(complete('--', ['volume', 'rm'])).resolves.toEqual(['--json', '--profile', '--help', '--yes']);
    await expect(complete('--pro', ['volume', 'rm'])).resolves.toEqual(['--profile']);
  });

  it('completes an option value with its completer, after the flag and after =', async () => {
    vi.mocked(loadConfig).mockResolvedValue({ profiles: { prod: {}, local: {}, staging: {} } } as never);

    await expect(complete('p', ['volume', 'ls', '--profile'])).resolves.toEqual(['prod']);
    await expect(complete('', ['volume', 'ls', '--profile'])).resolves.toEqual(['prod', 'local', 'staging']);
    await expect(complete('--profile=p', ['volume', 'ls'])).resolves.toEqual(['--profile=prod']);
  });

  it('completes nothing for an option without a completer', async () => {
    const context = ctx();
    const cloud = vi.spyOn(context, 'cloud');

    await expect(complete('', ['exec', '--name'], context)).resolves.toEqual([]);
    await expect(complete('--name=x', ['exec'], context)).resolves.toEqual([]);
    expect(cloud).not.toHaveBeenCalled();
  });

  it('completes an argument with its completer, after a flag that takes no value', async () => {
    await expect(complete('vol://da', ['volume', 'rm', '--yes'], ctx(withVolumes([DATASETS])))).resolves.toEqual(['vol://datasets/']);
  });

  it('completes every position of a variadic argument', async () => {
    await expect(complete('vol://da', ['volume', 'rm', 'vol://models/x'], ctx(withVolumes([DATASETS])))).resolves.toEqual(['vol://datasets/']);
  });

  it('completes nothing past the declared arguments or for an undeclared one', async () => {
    const context = ctx(withVolumes([DATASETS]));

    await expect(complete('vol://da', ['volume', 'ls', 'vol://models/'], context)).resolves.toEqual([]);
    await expect(complete('vol://da', ['get'], context)).resolves.toEqual([]);
  });

  it('completes Commander choices', async () => {
    await expect(complete('b', ['completion'])).resolves.toEqual(['bash']);
    await expect(complete('', ['completion'])).resolves.toEqual(['bash', 'zsh']);
  });

  it('lists commands after help', async () => {
    await expect(complete('v', ['help'])).resolves.toEqual(['volume']);
  });

  it('falls through to the argument when no subcommand matches', async () => {
    await expect(complete('vol', ['volume', 'ls'])).resolves.toEqual(['vol://']);
  });
});

describe('volumePaths', () => {
  it('completes volume names by case-insensitive prefix and uuid prefix', async () => {
    const context = ctx(withVolumes([DATASETS, MODELS]));

    await expect(volumePaths('vol://da', context)).resolves.toEqual(['vol://datasets/']);
    await expect(volumePaths('vol://DAT', context)).resolves.toEqual(['vol://datasets/']);
    await expect(volumePaths('vol://22222222', context)).resolves.toEqual(['vol://models/']);
  });

  it('lists all volumes with a trailing slash at the scheme', async () => {
    await expect(volumePaths('vol://', ctx(withVolumes([DATASETS, MODELS])))).resolves.toEqual(['vol://datasets/', 'vol://models/']);
  });

  it('completes partial scheme prefixes to vol://', async () => {
    await expect(volumePaths('vol', ctx())).resolves.toEqual(['vol://']);
    await expect(volumePaths('vol:', ctx())).resolves.toEqual(['vol://']);
    await expect(volumePaths('v', ctx())).resolves.toEqual([]);
  });

  it('lists folder entries, marking directories with a slash', async () => {
    entries([file('a.txt', 'a.txt'), dir('sub', 'sub')]);

    await expect(volumePaths('vol://datasets/', ctx(withVolumes([DATASETS])))).resolves.toEqual(['vol://datasets/a.txt', 'vol://datasets/sub/']);
  });

  it('filters by the in-progress name, case-sensitively', async () => {
    const context = ctx(withVolumes([DATASETS]));

    entries([file('a.txt', 'a.txt'), file('A.md', 'A.md'), dir('sub', 'sub')]);

    await expect(volumePaths('vol://datasets/a', context)).resolves.toEqual(['vol://datasets/a.txt']);
    await expect(volumePaths('vol://datasets/s', context)).resolves.toEqual(['vol://datasets/sub/']);
  });

  it('lists nested folders using the canonical volume name', async () => {
    const list = vi.fn(async (): Promise<FileEntry[]> => [file('x.bin', 'train/x.bin')]);

    listing(list);

    await expect(volumePaths('vol://dat/train/', ctx(withVolumes([DATASETS])))).resolves.toEqual(['vol://datasets/train/x.bin']);
    expect(list).toHaveBeenCalledWith(DATASETS.id, 'train');
  });

  it('caps listing at three matching volumes', async () => {
    const list = vi.fn(async (): Promise<FileEntry[]> => [file('x', 'x')]);
    const many = [1, 2, 3, 4].map(index => ({ id: `id-${index}`, name: `data${index}` }));

    listing(list);

    await expect(volumePaths('vol://data/x', ctx(withVolumes(many)))).resolves.toEqual(['vol://data1/x', 'vol://data2/x', 'vol://data3/x']);
    expect(list).toHaveBeenCalledTimes(3);
  });

  it('ignores volumes whose listing fails', async () => {
    listing(async volumeId => {
      if (volumeId === DATASETS.id) {
        throw new Error('forbidden');
      }

      return [file('ok.txt', 'ok.txt')];
    });

    await expect(volumePaths('vol://dat/', ctx(withVolumes([DATASETS, { id: 'x', name: 'datax' }])))).resolves.toEqual(['vol://datax/ok.txt']);
  });

  it('returns nothing for local words without contacting the API', async () => {
    const context = ctx(withVolumes([DATASETS]));
    const cloud = vi.spyOn(context, 'cloud');

    for (const word of ['./weights', 'vo']) {
      await expect(volumePaths(word, context)).resolves.toEqual([]);
    }

    expect(cloud).not.toHaveBeenCalled();
  });

  it('completes volume and entry names containing spaces with raw spaces', async () => {
    await expect(volumePaths('vol://my', ctx(withVolumes([MY_DATA, DATASETS])))).resolves.toEqual(['vol://my data/']);

    entries([dir('my data', 'my data')]);
    await expect(volumePaths('vol://datasets/my', ctx(withVolumes([DATASETS])))).resolves.toEqual(['vol://datasets/my data/']);

    entries([file('report 2024.txt', 'report 2024.txt')]);
    await expect(volumePaths('vol://datasets/report', ctx(withVolumes([DATASETS])))).resolves.toEqual(['vol://datasets/report 2024.txt']);
  });

  it('de-duplicates identical candidates', async () => {
    entries([file('x', 'x')]);

    await expect(volumePaths('vol://data/', ctx(withVolumes([DATASETS, { id: 'y', name: 'datax' }])))).resolves.toEqual(['vol://datasets/x', 'vol://datax/x']);
  });
});

describe('shared completers', () => {
  it('completes instance names and short ids by case-insensitive prefix', async () => {
    const context = ctx({ listInstances: async () => ({ items: [TMUX, DATASET_INSTANCE] }) });

    await expect(instances('tm', context)).resolves.toEqual(['tmux-bundle-ubuntu', 'tmxbnd1234']);
    await expect(instances('TMUX-B', context)).resolves.toEqual(['tmux-bundle-ubuntu']);
    await expect(instances('', context)).resolves.toEqual(['tmux-bundle-ubuntu', 'tmxbnd1234', 'datasets-prod', 'dtspr0d012']);
  });

  it('completes Run Job short ids', async () => {
    const context = ctx({ listJobs: async () => ({ items: [{ shortId: 'vgjggsynrr', name: 'train' }, { shortId: 'kq2mzx0bhd', name: 'eval' }] }) });

    await expect(jobs('vg', context)).resolves.toEqual(['vgjggsynrr']);
  });

  it('completes volume names, and mounts as name: until the colon is typed', async () => {
    const context = ctx(withVolumes([DATASETS, MY_DATA]));

    await expect(volumes('da', context)).resolves.toEqual(['datasets']);
    await expect(volumeMounts('', context)).resolves.toEqual(['datasets:', 'my data:']);
    await expect(volumeMounts('datasets:/w', context)).resolves.toEqual([]);
  });

  it('completes enabled template names and registry credential names', async () => {
    const context = ctx({
      templates: async () => [{ name: 'PyTorch', enabled: true }, { name: 'Retired', enabled: false }],
      registryCredentials: async () => [{ name: 'ghcr' }, { name: 'docker-hub' }],
    });

    await expect(templates('', context)).resolves.toEqual(['PyTorch']);
    await expect(registries('g', context)).resolves.toEqual(['ghcr']);
  });

  it('completes the GPU models, GPU counts and CPU sizes on offer for the workload', async () => {
    const gpu = (gpuModel: string, gpuCount: number) => ({ resourceType: 'run-job.gpu', unit: 'gpu_hour', gpuCount, resources: { gpuModel, cpuCores: 32 } });
    const cpu = (cores: number) => ({ resourceType: 'run-job.cpu', unit: 'vcpu_hour', gpuCount: null, resources: { gpuModel: null, cpuCores: cores } });
    const pricing = vi.fn(async () => [gpu('MI325X', 8), gpu('MI325X', 1), gpu('H100', 2), cpu(16), cpu(4), { resourceType: 'storage', unit: 'gib_month', gpuCount: null }]);
    const context = ctx({ pricing });

    await expect(gpuTypes('run-job')('', context)).resolves.toEqual(['mi325x', 'h100']);
    expect(pricing).toHaveBeenLastCalledWith('run-job');

    await expect(gpuCounts('instance')('', context)).resolves.toEqual(['1', '2', '8']);
    expect(pricing).toHaveBeenLastCalledWith('instance');

    await expect(cpuCores('instance')('1', context)).resolves.toEqual(['16']);
  });

  it('completes configured profile names', async () => {
    vi.mocked(loadConfig).mockResolvedValue({ profiles: { prod: {}, local: {} } } as never);

    await expect(profiles('l', ctx())).resolves.toEqual(['local']);
  });
});

describe('declared completers', () => {
  const program = new CliCommand('featherless-cloud');

  program
    .addCommand(loginCommand)
    .addCommand(instanceCommand)
    .addCommand(volumeCommand)
    .addCommand(pricingCommand)
    .addCommand(jobCommand)
    .addCommand(execCommand)
    .addCommand(completionCommand)
    .addCommand(completeCommand, { hidden: true });

  const tree = commandTree(program);
  const node = (command: string) => command.split(' ').reduce((current, name) => current.commands.find(child => child.name === name)!, tree);
  const argument = (command: string) => node(command).arguments[0]?.complete;
  const option = (command: string, flag: string) => node(command).options.find(candidate => candidate.flags.split(/[ ,]+/).includes(flag))?.complete;
  const values = async (complete: Completer | undefined, context = ctx()) => complete?.('', context);

  it.each([
    ...['get', 'update', 'logs', 'history', 'shell', 'ssh', 'tmux', 'start', 'stop', 'restart', 'delete'].map(name => [`instance ${name}`, instances] as const),
    ...['get', 'logs', 'cancel', 'delete'].map(name => [`job ${name}`, jobs] as const),
    ...['get', 'update', 'delete'].map(name => [`volume ${name}`, volumes] as const),
    ...['ls', 'find', 'mkdir', 'rm', 'cp', 'mv'].map(name => [`volume ${name}`, volumePaths] as const),
  ])('%s completes its argument', (command, completer) => {
    expect(argument(command)).toBe(completer);
  });

  it('completes option values', async () => {
    for (const command of ['instance create', 'exec']) {
      expect(option(command, '--template')).toBe(templates);
      expect(option(command, '--registry')).toBe(registries);
      expect(option(command, '--volume')).toBe(volumeMounts);
    }

    for (const command of ['login', 'instance list', 'instance ssh', 'job logs', 'volume cp', 'pricing list', 'exec']) {
      expect(option(command, '--profile'), command).toBe(profiles);
    }

    await expect(values(option('instance update', '--registry'), ctx({ registryCredentials: async () => [{ name: 'ghcr' }] }))).resolves.toEqual(['ghcr', 'null']);
  });

  it('completes recorded attempts of the job named by the first argument', async () => {
    const getJob = vi.fn(async () => ({
      attempts: [{ number: 1, containerRunId: 'a' }, { number: 2, containerRunId: null }, { number: 3, containerRunId: 'c' }],
    }));

    await expect(option('job logs', '--attempt')?.('', ctx({ getJob }, ['vgjggsynrr']))).resolves.toEqual(['1', '3']);
    expect(getJob).toHaveBeenCalledWith('vgjggsynrr');

    await expect(option('job logs', '--attempt')?.('', ctx({ getJob }))).resolves.toEqual([]);
    expect(getJob).toHaveBeenCalledTimes(1);
  });

  it('completes compute options from the workload the command runs', async () => {
    const pricing = vi.fn(async () => []);

    for (const [command, workload] of [['instance create', 'instance'], ['exec', 'run-job']]) {
      for (const flag of ['--gpu-type', '--gpu-count', '--cpu-cores']) {
        await values(option(command!, flag!), ctx({ pricing }));
        expect(pricing, `${command} ${flag}`).toHaveBeenLastCalledWith(workload);
      }
    }
  });

  it('completes fixed choices', async () => {
    await expect(values(option('instance create', '--compute-type'))).resolves.toEqual(['gpu', 'cpu']);
    await expect(values(option('volume find', '--type'))).resolves.toEqual(['f', 'd']);
    await expect(values(option('pricing list', '--workload'))).resolves.toEqual(['instance', 'run-job']);
    await expect(values(option('exec', '--preemption-retries'))).resolves.toEqual(['0', '1', '2', '3']);
    await expect(values(argument('completion'))).resolves.toEqual(['bash', 'zsh']);
  });
});

describe('profileFromCommand', () => {
  it('finds the last --profile value', () => {
    expect(profileFromCommand('featherless-cloud --profile prod volume ls vol://x')).toBe('prod');
    expect(profileFromCommand('featherless-cloud --profile=prod volume ls vol://x')).toBe('prod');
    expect(profileFromCommand('featherless-cloud --profile dev --profile prod volume ls vol://')).toBe('prod');
    expect(profileFromCommand('featherless-cloud volume ls vol://x')).toBeUndefined();
  });
});

/** Runs the bash completion function as readline does, with the CLI stubbed to print `candidates` and compopt recorded. */
function bashComplete(line: string, candidates: string[]): { reply: string[]; compopt: string[] } {
  const recorded = path.join(mkdtempSync(path.join(tmpdir(), 'fc-bash-')), 'compopt');

  const harness = [
    bashScript(),
    'featherless-cloud() { printf "%s\\n" "$STUB"; }',
    'compopt() { printf "%s\\n" "$*" >> "$RECORDED"; }',
    'COMP_WORDBREAKS=$WORDBREAKS COMP_LINE=$LINE COMP_POINT=${#LINE}',
    '_featherless_cloud_completions',
    'printf "%s\\n" "${COMPREPLY[@]}"',
  ].join('\n');

  const output = execFileSync('bash', ['--norc', '--noprofile', '-c', harness], {
    env: {
      PATH: process.env.PATH, LINE: line, STUB: candidates.join('\n'), WORDBREAKS: ' \t\n"\'><=;|&(:', RECORDED: recorded, COLUMNS: '40',
    },
    encoding: 'utf8',
  });

  let compopt = '';

  try {
    compopt = readFileSync(recorded, 'utf8');
  } catch {
    // compopt was never called.
  }

  return { reply: output.split('\n').filter(Boolean), compopt: compopt.split('\n').filter(Boolean) };
}

describe('bashScript', () => {
  it('replaces only the part of the word after the last : or = readline breaks on', () => {
    expect(bashComplete('featherless-cloud volume ls vol://da', ['vol://datasets/']).reply).toEqual(['//datasets/']);

    expect(bashComplete('featherless-cloud volume ls vol://datasets/my\\ data/re', ['vol://datasets/my data/report 2024.txt']).reply)
      .toEqual(['//datasets/my data/report 2024.txt']);

    expect(bashComplete('featherless-cloud volume ls --profile=p', ['--profile=prod']).reply).toEqual(['prod']);
    expect(bashComplete('featherless-cloud vol', ['volume']).reply).toEqual(['volume']);
  });

  it('keeps the whole word when the : is escaped or inside an open quote', () => {
    expect(bashComplete('featherless-cloud volume ls vol\\://da', ['vol://datasets/']).reply).toEqual(['vol://datasets/']);
    expect(bashComplete('featherless-cloud volume ls "vol://datasets/my d', ['vol://datasets/my data/']).reply).toEqual(['vol://datasets/my data/']);
    expect(bashComplete('featherless-cloud volume ls vol:"//datasets/my d', ['vol://datasets/my data/']).reply).toEqual(['//datasets/my data/']);
  });

  it('pads several names past half the screen so readline lists one per row', () => {
    expect(bashComplete('featherless-cloud instance s', ['shell', 'ssh', 'start']).reply).toEqual(['shell', 'ssh', 'start'].map(name => name.padEnd(20)));
    expect(bashComplete('featherless-cloud instance sh', ['shell']).reply).toEqual(['shell']);
    expect(bashComplete('featherless-cloud volume ls vol://datasets/', ['vol://datasets/a', 'vol://datasets/b/']).reply).toEqual(['//datasets/a', '//datasets/b/']);
  });

  it('adds no space after a folder or a name ending in :', () => {
    expect(bashComplete('featherless-cloud volume ls vol://da', ['vol://datasets/']).compopt).toEqual(['-o nospace']);
    expect(bashComplete('featherless-cloud instance create --volume da', ['datasets:']).compopt).toEqual(['-o nospace']);
    expect(bashComplete('featherless-cloud vol', ['volume']).compopt).toEqual([]);
  });
});

/** Autoloads the zsh completion function like compinit and runs it once, with compadd, _files and the CLI stubbed. */
function zshComplete(words: string[], candidates: string[]): { calls: string[]; cli: string } {
  const dir = mkdtempSync(path.join(tmpdir(), 'fc-zsh-'));

  writeFileSync(path.join(dir, '_featherless-cloud'), zshScript());

  const harness = [
    'compadd() {',
    '  local -a call',
    '  while (( $# )); do',
    '    if [[ $1 == -d ]]; then call+=(-d "${(j:,:)${(@P)2}}"); shift 2; else call+=("$1"); shift; fi',
    '  done',
    '  print -r -- "compadd ${(j: :)${(@qqq)call}}"',
    '}',
    '_files() { print -r -- _files; }',
    'compdef() { print -r -- "compdef $*"; }',
    'featherless-cloud() { print -r -- "$COMP_LINE|$FC_WORD" >| $DIR/cli; print -r -- "$STUB"; }',
    'fpath=($DIR $fpath)',
    'autoload -Uz _featherless-cloud',
    'words=("${(@f)WORDS}") CURRENT=${#${(@f)WORDS}} PREFIX=${${(@f)WORDS}[-1]}',
    'BUFFER=${(j: :)words} CURSOR=${#${(j: :)words}}',
    '_featherless-cloud',
  ].join('\n');

  const output = execFileSync('zsh', ['-f', '-c', harness], {
    env: { PATH: process.env.PATH, DIR: dir, WORDS: words.join('\n'), STUB: candidates.join('\n') },
    encoding: 'utf8',
  });

  let cli = '';

  try {
    cli = readFileSync(path.join(dir, 'cli'), 'utf8').trimEnd();
  } catch {
    // The CLI was never called.
  }

  return { calls: output.split('\n').filter(Boolean), cli };
}

describe('zshScript', () => {
  it('completes on the first Tab after compinit autoloads it', () => {
    expect(zshComplete(['featherless-cloud', 'vol'], ['volume'])).toEqual({
      calls: ['compadd "-l" "-d" "volume" "--" "volume"'],
      cli: 'featherless-cloud |vol',
    });
  });

  it('passes the word unquoted and shows only the last path component', () => {
    expect(zshComplete(['featherless-cloud', 'volume', 'ls', 'vol://datasets/my\\ d'], ['vol://datasets/my data/', 'vol://datasets/my data.txt']))
      .toEqual({
        calls: [
          'compadd "-l" "-S" "" "-d" "my data/" "--" "vol://datasets/my data/"',
          'compadd "-l" "-d" "my data.txt" "--" "vol://datasets/my data.txt"',
        ],
        cli: 'featherless-cloud volume ls |vol://datasets/my d',
      });

    expect(zshComplete(['featherless-cloud', 'volume', 'ls', 'vol://'], ['vol://datasets/']).calls)
      .toEqual(['compadd "-l" "-S" "" "-d" "datasets/" "--" "vol://datasets/"']);

    expect(zshComplete(['featherless-cloud', 'volume', 'ls', 'vol'], ['vol://']).calls)
      .toEqual(['compadd "-l" "-S" "" "-d" "vol://" "--" "vol://"']);
  });

  it('adds no space after a name ending in :', () => {
    expect(zshComplete(['featherless-cloud', 'instance', 'create', '--volume', 'da'], ['datasets:']).calls)
      .toEqual(['compadd "-l" "-S" "" "-d" "datasets:" "--" "datasets:"']);
  });

  it('falls back to local files when the CLI has nothing', () => {
    expect(zshComplete(['featherless-cloud', 'volume', 'cp', './we'], []).calls).toEqual(['_files']);
  });

  it('registers itself with compdef when sourced', () => {
    const output = execFileSync('zsh', ['-f', '-c', 'compdef() { print -r -- "compdef $*"; }\neval "$SCRIPT"'], {
      env: { PATH: process.env.PATH, SCRIPT: zshScript() },
      encoding: 'utf8',
    });

    expect(output.trim()).toBe('compdef _featherless-cloud featherless-cloud');
  });
});
