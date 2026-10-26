import { CliCommand } from '../../completions';
import { registryKeysListCommand } from './list';
import { registryKeysAddCommand } from './add';
import { registryKeysUpdateCommand } from './update';
import { registryKeysDeleteCommand } from './delete';

export const registryKeysCommand = new CliCommand('registry-keys')
  .description('Manage container registry logins for private images')
  .addCommand(registryKeysListCommand)
  .addCommand(registryKeysAddCommand)
  .addCommand(registryKeysUpdateCommand)
  .addCommand(registryKeysDeleteCommand);
