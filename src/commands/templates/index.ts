import { CliCommand } from '../../completions';
import { templatesListCommand } from './list';
import { templatesShowCommand } from './show';
import { templatesCreateCommand } from './create';
import { templatesUpdateCommand } from './update';
import { templatesDeleteCommand } from './delete';

export const templatesCommand = new CliCommand('templates')
  .description('Save launch settings and launch instances and Run Jobs from them')
  .addCommand(templatesListCommand)
  .addCommand(templatesShowCommand)
  .addCommand(templatesCreateCommand)
  .addCommand(templatesUpdateCommand)
  .addCommand(templatesDeleteCommand);
