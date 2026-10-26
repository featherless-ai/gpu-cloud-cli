import { CliCommand } from '../../completions';
import { jobListCommand } from './list';
import { jobGetCommand } from './get';
import { jobLogsCommand } from './logs';
import { jobCancelCommand, jobDeleteCommand } from './action';

export const jobCommand = new CliCommand('runjob')
  .alias('job')
  .description('Run and manage Run Jobs')
  .addCommand(jobListCommand)
  .addCommand(jobGetCommand)
  .addCommand(jobLogsCommand)
  .addCommand(jobCancelCommand)
  .addCommand(jobDeleteCommand);
