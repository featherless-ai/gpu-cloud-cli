import type { CliCommand } from '../../completions';
import { registerVolumeGetCommand } from './get';
import { registerVolumeCreateCommand } from './create';
import { registerVolumeUpdateCommand } from './update';
import { registerVolumeDeleteCommand } from './delete';
import { registerVolumeFileCommands } from './files';
import { registerVolumeSyncCommand } from './sync';

export function registerVolumeCommand(program: CliCommand): void {
  const volume = program
    .command('volume')
    .description('Manage network storage volumes');

  registerVolumeGetCommand(volume);
  registerVolumeCreateCommand(volume);
  registerVolumeUpdateCommand(volume);
  registerVolumeDeleteCommand(volume);
  registerVolumeFileCommands(volume);
  registerVolumeSyncCommand(volume);
}
