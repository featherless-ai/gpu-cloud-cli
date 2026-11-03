import type { Command } from 'commander';
import { CliCommand } from '../../completions';
import { assertInstanceName, profiles, resolveTemplateRef, templates } from '../shared';
import { createContext } from '../../context';
import { resolveAuth } from '../../config';
import { CloudClient } from '../../clients/cloud';
import { CliError } from '../../error';
import type { Template } from '../../types/templates';
import { templateBody, withTemplateFlags, type TemplateFlags } from './body';

export const templatesUpdateCommand = withTemplateFlags(new CliCommand('update')
  .argument('<template>', 'template name or id', { complete: templates })
  .description('Change a template; instances and Run Jobs already launched keep their settings')
  .option('--name <name>', 'new name'))
  .option('--profile <name>', 'profile to use', { complete: profiles })
  .action(async (ref: string, options: TemplateFlags & { name?: string; profile?: string }, cmd: Command) => {
    const ctx = createContext(cmd);
    const auth = await resolveAuth(options.profile);
    const client = new CloudClient(auth.apiBase, auth.apiKey);

    const template = resolveTemplateRef(await ctx.spin('Finding template', () => client.templates()), ref);
    const body = await templateBody(client, options, cmd, ctx);

    if (options.name) {
      assertInstanceName(options.name);
      body.name = options.name;
    }

    if (Object.keys(body).length === 0) {
      throw new CliError('Nothing to change. Pass the settings to change, as for templates create.');
    }

    const updated = await ctx.spin('Updating template', () => client.updateTemplate(template.id, body));

    ctx.success(`Updated template ${updated.name}.`);
    ctx.output(updated, (item: Template) => ({ text: item.shortId }));
  });
