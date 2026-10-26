import { CliCommand } from '../../completions';
import { instanceListCommand } from './list';
import { instanceGetCommand } from './get';
import { instanceCreateCommand } from './create';
import { instanceUpdateCommand } from './update';
import { instanceStartCommand, instanceStopCommand, instanceRestartCommand, instanceDeleteCommand } from './action';
import { instanceLogsCommand } from './logs';
import { instanceHistoryCommand } from './history';
import { instanceShellCommand } from './shell';
import { instanceSshCommand, instanceTmuxCommand } from './ssh';

export const instanceCommand = new CliCommand('instance')
  .description('Manage GPU instances')
  .addCommand(instanceListCommand)
  .addCommand(instanceGetCommand)
  .addCommand(instanceCreateCommand)
  .addCommand(instanceUpdateCommand)
  .addCommand(instanceStartCommand)
  .addCommand(instanceStopCommand)
  .addCommand(instanceRestartCommand)
  .addCommand(instanceDeleteCommand)
  .addCommand(instanceLogsCommand)
  .addCommand(instanceHistoryCommand)
  .addCommand(instanceShellCommand)
  .addCommand(instanceSshCommand)
  .addCommand(instanceTmuxCommand);
