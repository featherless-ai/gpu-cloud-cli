import { loginCommand } from './commands/login';
import { logoutCommand } from './commands/logout';
import { instanceCommand } from './commands/instance';
import { volumeCommand } from './commands/volume';
import { pricingCommand } from './commands/pricing';
import { billingCommand } from './commands/billing';
import { jobCommand } from './commands/job';
import { execCommand } from './commands/exec';
import { sshKeysCommand } from './commands/ssh-keys';
import { registryKeysCommand } from './commands/registry-keys';
import { upgradeCommand } from './commands/upgrade';
import { completeCommand, completionCommand } from './commands/completion';
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
  .version(VERSION);

program
  .addCommand(loginCommand)
  .addCommand(logoutCommand)
  .addCommand(instanceCommand)
  .addCommand(volumeCommand)
  .addCommand(pricingCommand)
  .addCommand(billingCommand)
  .addCommand(jobCommand)
  .addCommand(execCommand)
  .addCommand(sshKeysCommand)
  .addCommand(registryKeysCommand)
  .addCommand(upgradeCommand)
  .addCommand(completionCommand)
  .addCommand(completeCommand, { hidden: true })
  .addCommand(new CliCommand('_check-update').action(runUpdateCheck), { hidden: true });

await offerUpdate();
program.parse();
