import { CliCommand } from '../../completions';
import { pricingListCommand } from './list';

export const pricingCommand = new CliCommand('pricing')
  .description('Browse compute and storage prices')
  .addCommand(pricingListCommand);
