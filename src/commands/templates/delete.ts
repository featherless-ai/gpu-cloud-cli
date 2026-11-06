import type { Command } from 'commander';
import { CliCommand } from '../../completions';
import { createContext } from '../../context';
import { resolveAuth } from '../../config';
import { CloudClient } from '../../clients/cloud';
import { profiles, requireDeletion, resolveTemplateRef, templates } from '../shared';

export const templatesDeleteCommand = new CliCommand('delete')
  .argument('<template>', 'template name or id', { complete: templates })
  .description('Delete a template; instances and Run Jobs launched from it keep running')
  .option('--yes', 'skip confirmation')
  .option('--profile <name>', 'profile to use', { complete: profiles })
  .action(async (ref: string, options: { yes?: boolean; profile?: string }, cmd: Command) => {
    const ctx = createContext(cmd);
    const auth = await resolveAuth(options.profile);
    const client = new CloudClient(auth.apiBase, auth.apiKey);

    const template = resolveTemplateRef(await ctx.spin('Finding template', () => client.templates()), ref);

    await requireDeletion(ctx, `template ${template.name}`, Boolean(options.yes));
    await ctx.spin('Deleting template', () => client.deleteTemplate(template.id));

    ctx.success(`Deleted template ${template.name}.`);
  });
