import type { CliCommand } from '../../completions';
import { registerJobListCommand } from './list';
import { registerJobGetCommand } from './get';
import { registerJobLogsCommand } from './logs';
import { registerJobActionCommands } from './action';

export function registerJobCommand(parent: CliCommand): void {
  const job = parent
    .command('runjob')
    .alias('job')
    .description('Run and manage Run Jobs');

  registerJobListCommand(job);
  registerJobGetCommand(job);
  registerJobLogsCommand(job);
  registerJobActionCommands(job);
}
