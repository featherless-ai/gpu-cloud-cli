import type { CliCommand } from '../../completions';
import { registerInstancesListCommand } from './list';
import { registerInstanceGetCommand } from './get';
import { registerInstanceCreateCommand } from './create';
import { registerInstanceUpdateCommand } from './update';
import { registerInstanceActionCommands } from './action';
import { registerInstanceLogsCommand } from './logs';
import { registerInstanceHistoryCommand } from './history';
import { registerInstanceShellCommand } from './shell';
import { registerInstanceSshCommands } from './ssh';

export function registerInstanceCommand(parent: CliCommand): void {
  const instance = parent
    .command('instance')
    .description('Manage GPU instances');

  registerInstancesListCommand(instance);
  registerInstanceGetCommand(instance);
  registerInstanceCreateCommand(instance);
  registerInstanceUpdateCommand(instance);
  registerInstanceActionCommands(instance);
  registerInstanceLogsCommand(instance);
  registerInstanceHistoryCommand(instance);
  registerInstanceShellCommand(instance);
  registerInstanceSshCommands(instance);
}
