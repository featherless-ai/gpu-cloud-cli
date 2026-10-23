import { registerLoginCommand } from './commands/login';
import { registerLogoutCommand } from './commands/logout';
import { registerInstanceCommand } from './commands/instance';
import { registerVolumeCommand } from './commands/volume';
import { registerPricingCommand } from './commands/pricing';
import { registerBillingCommand } from './commands/billing';
import { registerJobCommand } from './commands/job';
import { registerExecCommand } from './commands/exec';
import { registerUpgradeCommand } from './commands/upgrade';
import { registerCompletionCommand } from './commands/completion';
import { offerUpdate, runUpdateCheck } from './update-check';
import { describeError } from './error';
import { APP_NAME, VERSION } from './version';
import { CliCommand } from './completions';

const program = new CliCommand();

function handleError(err: unknown): void {
  const message = describeError(err);
  const isJson = program.opts().json;

  if (isJson) {
    console.log(JSON.stringify({ error: message }));
  } else {
    console.error(`Error: ${message}`);
  }

  process.exit(1);
}

process.on('uncaughtException', handleError);
process.on('unhandledRejection', handleError);

program
  .name(APP_NAME)
  .description('Featherless GPU Cloud CLI')
  .option('--json', 'output as JSON (for scripts and agents)')
  .option('--non-interactive', 'disable spinners and prompts')
  .version(VERSION)
  .configureHelp({ showGlobalOptions: true });

registerLoginCommand(program);
registerLogoutCommand(program);
registerInstanceCommand(program);
registerVolumeCommand(program);
registerPricingCommand(program);
registerBillingCommand(program);
registerJobCommand(program);
registerExecCommand(program);
registerUpgradeCommand(program);

registerCompletionCommand(program);

program
  .command('_check-update', { hidden: true })
  .action(async () => {
    await runUpdateCheck();
  });

await offerUpdate();
program.parse();
