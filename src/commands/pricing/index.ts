import type { CliCommand } from '../../completions';
import { registerPricingListCommand } from './list';

export function registerPricingCommand(program: CliCommand): void {
  const pricing = program
    .command('pricing')
    .description('Browse compute and storage prices');

  registerPricingListCommand(pricing);
}
