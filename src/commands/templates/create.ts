import type { Command } from 'commander';
import { CliCommand } from '../../completions';
import { assertInstanceName, profiles } from '../shared';
import { createContext } from '../../context';
import { resolveAuth } from '../../config';
import { CloudClient } from '../../clients/cloud';
import { CliError } from '../../error';
import type { Template } from '../../types/templates';
import { templateBody, withTemplateFlags, type TemplateFlags } from './body';

export const templatesCreateCommand = withTemplateFlags(new CliCommand('create')
  .argument('<name>', 'lowercase letters, numbers and hyphens; what you launch it by')
  .description('Save launch settings as a template, private unless --visibility organization'))
  .option('--profile <name>', 'profile to use', { complete: profiles })
  .addHelpText('after', '\nExamples:\n  featherless-cloud templates create trainer --image ghcr.io/acme/trainer:1 --gpu-count 1 --env EPOCHS=3\n  featherless-cloud instance create --template trainer')
  .action(async (name: string, options: TemplateFlags & { profile?: string }, cmd: Command) => {
    assertInstanceName(name);

    if (!options.template && !options.image) {
      throw new CliError('Pass --image, or --template to start from an official template.');
    }

    const ctx = createContext(cmd);
    const auth = await resolveAuth(options.profile);
    const client = new CloudClient(auth.apiBase, auth.apiKey);

    const body = await templateBody(client, options, cmd, ctx);
    const template = await ctx.spin('Saving template', () => client.createTemplate({ ...body, name }));

    ctx.success(`Saved template ${template.name}. Launch it with featherless-cloud instance create --template ${template.name}.`);
    ctx.output(template, (item: Template) => ({ text: item.shortId }));
  });
