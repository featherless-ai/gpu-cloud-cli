import type { CliCommand } from '../../completions';
import { registerBillingSettingsCommand } from './settings';
import { registerBillingHistoryCommand } from './history';

export function registerBillingCommand(program: CliCommand): void {
  const billing = program
    .command('billing')
    .description('Show balance and usage history');

  registerBillingSettingsCommand(billing);
  registerBillingHistoryCommand(billing);
}
