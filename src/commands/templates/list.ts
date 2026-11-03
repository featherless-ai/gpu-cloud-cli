import type { Command } from 'commander';
import { CliCommand } from '../../completions';
import { profiles, templateReference } from '../shared';
import { createContext } from '../../context';
import { resolveAuth } from '../../config';
import { CloudClient } from '../../clients/cloud';
import type { Template } from '../../types/templates';

export const templatesListCommand = new CliCommand('list')
  .description('List official templates, yours, and those shared in your organization')
  .option('--profile <name>', 'profile to use', { complete: profiles })
  .action(async (options: { profile?: string }, cmd: Command) => {
    const ctx = createContext(cmd);
    const auth = await resolveAuth(options.profile);
    const client = new CloudClient(auth.apiBase, auth.apiKey);

    const [templates, pricing] = await ctx.spin('Fetching templates', () => Promise.all([client.templates(), client.pricing()]));
    const computeName = (sku: string | null) => pricing.find(rate => rate.sku === sku)?.displayName ?? '-';

    ctx.output(templates, (list: Template[]) => ({
      table: {
        header: ['NAME', 'VISIBILITY', 'OWNER', 'IMAGE', 'COMPUTE', 'ID'],
        rows: list.map(template => [
          templateReference(template, list),
          template.visibility,
          template.owner?.name ?? 'Featherless',
          template.image,
          computeName(template.computeSku),
          template.shortId,
        ]),
      },
    }));
  });
