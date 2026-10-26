import { CliCommand } from '../../completions';
import { sshKeysListCommand } from './list';
import { sshKeysAddCommand } from './add';
import { sshKeysDeleteCommand } from './delete';

export const sshKeysCommand = new CliCommand('ssh-keys')
  .description('Manage your SSH public keys')
  .addCommand(sshKeysListCommand)
  .addCommand(sshKeysAddCommand)
  .addCommand(sshKeysDeleteCommand);
