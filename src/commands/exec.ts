import type { Command } from 'commander';
import { CliCommand } from '../completions';
import { CloudClient, type CreateRunJobBody } from '../clients/cloud';
import { resolveAuth } from '../config';
import { createContext } from '../context';
import { CliError, RetryableError } from '../error';
import type { RunJobDto, RunJobState } from '../types/job';
import {
  assertInstanceName,
  findTemplate,
  cpuCores,
  gpuCounts,
  gpuTypes,
  parseDisk,
  parseRepeatable,
  parseVolumeFlags,
  profiles,
  randomInstanceName,
  registries,
  resolveCompute,
  resolveImage,
  resolveVolume,
  templateIds,
  templates,
  toEnvironment,
  validateMounts,
  volumeMounts,
} from './shared';
import { newContent, nextLogCursor } from './instance/logs';

const TERMINAL_STATES = new Set<RunJobState>(['succeeded', 'failed', 'cancelled', 'timed_out', 'interrupted']);
const POLL_MS = 2_000;
const MAX_RECONNECT_DELAY_MS = 10_000;

interface ExecOptions {
  name?: string;
  image?: string;
  template?: string;
  templateId?: string;
  registry?: string;
  gpuType?: string;
  gpuCount?: string;
  cpuCores?: string;
  disk: string;
  volume?: string[];
  workdir: string;
  env?: string[];
  secret?: string[];
  timeout: string;
  retainLogs: boolean;
  preemptionRetries: string;
  idempotencyKey?: string;
  detach?: boolean;
  cancelOnInterrupt?: boolean;
  profile?: string;
}

function boundedInteger(value: string, label: string, min: number, max: number): number {
  const parsed = Number(value);

  if (!Number.isInteger(parsed) || parsed < min || parsed > max) {
    throw new CliError(`${label} must be between ${min} and ${max}.`);
  }

  return parsed;
}

function status(message: string): void {
  process.stderr.write(`${message}\n`);
}

const sleep = (ms: number): Promise<void> => new Promise(resolve => setTimeout(resolve, ms));

export function remoteExitCode(job: RunJobDto): number {
  if (job.state === 'succeeded') {
    return job.exitCode ?? 0;
  }

  if (job.state === 'timed_out') {
    return 124;
  }

  if (job.state === 'cancelled') {
    return 130;
  }

  return job.exitCode ?? 1;
}

async function attach(client: CloudClient, initial: RunJobDto, cancelOnInterrupt: boolean): Promise<RunJobDto> {
  let job = initial;
  let previousState: RunJobState | null = null;
  let attempt = initial.attemptNumber;
  let previousLogs = '';
  let interrupted = false;
  let reconnectAttempts = 0;
  let reconnecting = false;
  let terminalObservedAt: number | undefined;

  const interrupt = () => {
    interrupted = true;
  };

  process.once('SIGINT', interrupt);

  try {
    for (;;) {
      try {
        if (job.attemptNumber !== attempt) {
          status(`Run Job ${job.shortId}: starting again from the beginning (attempt ${job.attemptNumber})`);
          attempt = job.attemptNumber;
          previousLogs = '';
        }

        if (job.state !== previousState) {
          status(`Run Job ${job.shortId}: ${job.state}`);
          previousState = job.state;
        }

        let logsComplete = false;

        if (job.state === 'running' || TERMINAL_STATES.has(job.state)) {
          const latest = await client.jobLogs(job.id, 5000);
          const delta = newContent(previousLogs, latest.logs);

          if (delta) {
            process.stdout.write(delta);
          }

          // Collection is asynchronous; an empty fetch does not reset overlap.
          previousLogs = nextLogCursor(previousLogs, latest.logs, latest.complete);
          logsComplete = latest.complete;

          if (latest.truncated) {
            status(`Run Job ${job.shortId}: only recent output is shown; browse log history for earlier lines.`);
          }
        }

        if (TERMINAL_STATES.has(job.state) && (logsComplete || !job.retainLogs)) {
          return job;
        }

        if (TERMINAL_STATES.has(job.state)) {
          terminalObservedAt ??= Date.now();

          if (Date.now() - terminalObservedAt >= 10_000) {
            status(`Job ${job.shortId}: finished. More output may arrive; use featherless-cloud job logs ${job.shortId} to check again.`);
            return job;
          }
        }

        if (interrupted) {
          if (cancelOnInterrupt) {
            await client.cancelJob(job.id);
            status(`Run Job ${job.shortId}: cancellation requested.`);
          } else {
            status(`Detached from Run Job ${job.shortId}. Reattach with featherless-cloud runjob logs ${job.shortId} --follow.`);
          }

          process.exitCode = 130;
          return job;
        }

        await sleep(POLL_MS);
        job = await client.getJob(job.id);

        if (reconnecting) {
          status(`Run Job ${job.shortId}: connection restored.`);
        }

        reconnecting = false;
        reconnectAttempts = 0;
      } catch (error) {
        if (!(error instanceof RetryableError)) {
          throw error;
        }

        if (!reconnecting) {
          status(`Run Job ${job.shortId}: connection lost; reconnecting.`);
          reconnecting = true;
        }

        if (interrupted) {
          status(`Detached from Run Job ${job.shortId}. Reattach with featherless-cloud runjob logs ${job.shortId} --follow.`);
          process.exitCode = 130;
          return job;
        }

        const delay = Math.min(1_000 * 2 ** reconnectAttempts, MAX_RECONNECT_DELAY_MS);
        reconnectAttempts += 1;
        await sleep(delay);
      }
    }
  } finally {
    process.off('SIGINT', interrupt);
  }
}

export const execCommand = new CliCommand('exec')
  .argument('[command...]')
  .description('Run a command on temporary CPU or GPU compute')
  .option('--name <name>', 'Run Job name (random when omitted)')
  .option('--template <name>', 'template: a name, org/<name>, featherless/<name> or an id; defaults to the PyTorch environment', { complete: templates })
  .option('--template-id <id>', 'template, by id', { complete: templateIds })
  .option('--image <reference>', 'container image to run, or to replace the template\'s')
  .option('--registry <name|id>', 'registry credential for a private image', { complete: registries })
  .option('--gpu-type <text>', 'GPU model substring, e.g. mi325x', { complete: gpuTypes('run-job') })
  .option('--gpu-count <n>', 'GPUs: 1, 2, 4, or 8', { complete: gpuCounts('run-job') })
  .option('--cpu-cores <n>', 'vCPU cores; mutually exclusive with GPU options', { complete: cpuCores('run-job') })
  .option('--disk <gb>', 'container disk in GB (20-2000)', '20')
  .option('--volume <name:path>', 'mount a network volume, repeatable', { parse: (v: string, prev: string[]) => [...prev, v], defaultValue: [], complete: volumeMounts })
  .option('--workdir <path>', 'container working directory', '/workspace')
  .option('--env <K=V>', 'environment variable, repeatable', (v: string, prev: string[]) => [...prev, v], [])
  .option('--secret <K=V>', 'secret variable, repeatable', (v: string, prev: string[]) => [...prev, v], [])
  .option('--timeout <seconds>', 'run time limit after compute assignment (1-86400)', '86400')
  .option('--preemption-retries <n>', 'start again from the beginning up to n times if interrupted for other work (0-3)', { defaultValue: '0', complete: () => ['0', '1', '2', '3'] })
  .option('--no-retain-logs', 'disable output collection, including live attached output')
  .option('--idempotency-key <key>', 'retry-safe execution key (random when omitted)')
  .option('--detach', 'submit without waiting for the result')
  .option('--cancel-on-interrupt', 'cancel instead of detach when Ctrl-C is pressed')
  .option('--profile <name>', 'authentication profile to use', { complete: profiles })
  .addHelpText('after', '\nExamples:\n  featherless-cloud exec --gpu-count 1 -- python -c \'import torch; print(torch.cuda.is_available())\'\n  featherless-cloud exec --cpu-cores 32 -- python -c \'import os; print(os.cpu_count())\'')
  .action(async (command: string[], options: ExecOptions, cmd: Command) => {
    if (options.cpuCores && (options.gpuCount || options.gpuType)) {
      throw new CliError('--cpu-cores cannot be combined with GPU options.');
    }

    const ctx = createContext(cmd);

    if (ctx.mode === 'json' && !options.detach) {
      throw new CliError('--json can only be used with --detach; attached stdout is reserved for command output.');
    }

    const auth = await resolveAuth(options.profile);
    const client = new CloudClient(auth.apiBase, auth.apiKey);
    const name = options.name ?? randomInstanceName();
    assertInstanceName(name);

    const [template, pricing, volumes, credentials] = await ctx.spin('Loading execution options', () => Promise.all([
      findTemplate(client, options),
      client.pricing('run-job'),
      options.volume?.length ? client.listVolumes() : Promise.resolve([]),
      options.registry ? client.registryCredentials() : Promise.resolve([]),
    ]));

    if (command.length === 0 && !template?.command.length) {
      throw new CliError('Provide the command after --.');
    }

    // Without a template or image the server runs the PyTorch environment.
    const image = options.image || options.registry
      ? resolveImage(credentials, options, Boolean(template))
      : {};

    // A template's compute stands unless a flag says otherwise.
    const usesTemplateCompute = Boolean(template?.computeSku) && !options.gpuType && !options.gpuCount && !options.cpuCores;

    const compute = usesTemplateCompute ? {} : await resolveCompute(pricing, {
      computeType: options.cpuCores ? 'cpu' : 'gpu',
      gpuType: options.gpuType,
      gpuCount: options.gpuCount,
      cpuCores: options.cpuCores,
      interactive: false,
    }, ctx);

    const mounts = parseVolumeFlags(options.volume).map(entry => ({
      networkVolumeId: resolveVolume(volumes, entry.volume),
      mountPath: entry.mountPath,
    }));

    if (mounts.length > 8) {
      throw new CliError('At most 8 network volumes can be mounted.');
    }

    validateMounts(mounts);

    const body: CreateRunJobBody = {
      name,
      ...(template ? { templateId: template.id } : {}),
      ...image,
      ...compute,
      ...(command.length ? { command } : {}),
      ...(!template || cmd.getOptionValueSource('disk') === 'cli' ? { ephemeralDiskGiB: parseDisk(options.disk) } : {}),
      workingDirectory: options.workdir,
      timeoutSeconds: boundedInteger(options.timeout, 'Timeout', 1, 86_400),
      preemptionRetries: boundedInteger(options.preemptionRetries, 'Preemption retries', 0, 3),
      retainLogs: options.retainLogs,
    };

    if (mounts.length) {
      body.networkVolumeMounts = mounts;
    }

    const environment = toEnvironment(parseRepeatable(options.env, 'env'));

    if (environment.length) {
      body.environment = environment;
    }

    const secrets = toEnvironment(parseRepeatable(options.secret, 'secret'));

    if (secrets.length) {
      body.secrets = secrets;
    }

    const job = await ctx.spin('Starting command', () => client.createJob(
      body,
      options.idempotencyKey ?? crypto.randomUUID(),
    ));

    if (options.detach) {
      ctx.output(job, item => ({ text: item.shortId }));
      return;
    }

    const completed = await attach(client, job, Boolean(options.cancelOnInterrupt));

    if (TERMINAL_STATES.has(completed.state)) {
      process.exitCode = remoteExitCode(completed);
    }
  });
