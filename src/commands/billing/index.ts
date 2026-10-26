import { CliCommand } from '../../completions';
import { billingSettingsCommand } from './settings';
import { billingHistoryCommand } from './history';

export const billingCommand = new CliCommand('billing')
  .description('Show balance and usage history')
  .addCommand(billingSettingsCommand)
  .addCommand(billingHistoryCommand);
