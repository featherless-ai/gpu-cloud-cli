import { CliCommand } from '../../completions';
import { volumeGetCommand } from './get';
import { volumeCreateCommand } from './create';
import { volumeUpdateCommand } from './update';
import { volumeDeleteCommand } from './delete';
import {
  volumeLsCommand, volumeFindCommand, volumeMkdirCommand, volumeRmCommand, volumeCpCommand, volumeMvCommand,
} from './files';
import { volumeSyncCommand } from './sync';

export const volumeCommand = new CliCommand('volume')
  .description('Manage network storage volumes')
  .addCommand(volumeGetCommand)
  .addCommand(volumeCreateCommand)
  .addCommand(volumeUpdateCommand)
  .addCommand(volumeDeleteCommand)
  .addCommand(volumeLsCommand)
  .addCommand(volumeFindCommand)
  .addCommand(volumeMkdirCommand)
  .addCommand(volumeRmCommand)
  .addCommand(volumeCpCommand)
  .addCommand(volumeMvCommand)
  .addCommand(volumeSyncCommand);
