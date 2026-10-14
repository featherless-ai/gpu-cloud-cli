import type { Command } from 'commander';
import { resolveAuth } from '../config';
import { CloudClient } from '../clients/cloud';
import {
  bashScript, commandTree, completeWord, profileFromCommand, tokenizeLine, walkCommandTree, zshScript, type CliCommand, type CompleterContext,
} from '../completions';

export function registerCompletionCommand(program: CliCommand): void {
  program
    .command('completion')
    .argument('<shell>', 'bash or zsh', { choices: ['bash', 'zsh'] })
    .description('Print a shell completion script (bash, zsh)')
    .action((shell: string) => {
      console.log(shell === 'bash' ? bashScript() : zshScript());
    });

  program
    // FC_WORD carries the in-progress word; words starting with `-` would
    // be parsed as options if passed as argv.
    .command('_complete', { hidden: true })
    .action(async (_options: unknown, cmd: Command) => {
      // Completion runs on every Tab: failures stay silent so the shell
      // falls back to its own filename completion.
      try {
        const root = cmd.parent ?? cmd;
        const point = Number(process.env.COMP_POINT);
        const line = (process.env.COMP_LINE ?? '').slice(0, Number.isFinite(point) && point >= 0 ? point : undefined);
        const { words, open } = tokenizeLine(line);
        const current = process.env.FC_WORD ?? (open ? words.at(-1) ?? '' : '');
        const position = walkCommandTree(commandTree(root), words.slice(1, open ? -1 : undefined));
        const profile = profileFromCommand(line);
        let cloud: Promise<CloudClient> | undefined;

        const ctx: CompleterContext = {
          profile,
          args: position.args,
          cloud: () => cloud ??= resolveAuth(profile).then(auth => new CloudClient(auth.apiBase, auth.apiKey)),
        };

        for (const candidate of await completeWord(current, position, ctx)) {
          console.log(candidate);
        }
      } catch {
        // No completions; the shell completes local files instead.
      }
    });
}
