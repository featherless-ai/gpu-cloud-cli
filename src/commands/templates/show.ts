import type { Command } from 'commander';
import { CliCommand } from '../../completions';
import { profiles, resolveTemplateRef, templateReference, templates } from '../shared';
import { createContext } from '../../context';
import { resolveAuth } from '../../config';
import { CloudClient } from '../../clients/cloud';
import type { Template } from '../../types/templates';

export const templatesShowCommand = new CliCommand('show')
  .argument('<template>', 'template name or id', { complete: templates })
  .description('Show a template\'s settings')
  .option('--profile <name>', 'profile to use', { complete: profiles })
  .action(async (ref: string, options: { profile?: string }, cmd: Command) => {
    const ctx = createContext(cmd);
    const auth = await resolveAuth(options.profile);
    const client = new CloudClient(auth.apiBase, auth.apiKey);

    const [all, pricing] = await ctx.spin('Fetching template', () => Promise.all([client.templates(), client.pricing()]));
    const template = resolveTemplateRef(all, ref);

    ctx.output(template, (item: Template) => {
      const lines = [
        `NAME        ${templateReference(item, all)}`,
        `ID          ${item.shortId}`,
        `VISIBILITY  ${item.visibility}`,
        `OWNER       ${item.owner?.name ?? 'Featherless'}`,
        `IMAGE       ${item.image}`,
        `COMPUTE     ${pricing.find(rate => rate.sku === item.computeSku)?.displayName ?? '-'}`,
        `DISK        ${item.ephemeralDiskGiB ? `${item.ephemeralDiskGiB} GB` : '-'}`,
        `COMMAND     ${item.command.join(' ') || '-'}`,
        `ENV         ${item.environment.map(entry => `${entry.name}=${entry.value}`).join(' ') || '-'}`,
        `SECRETS     ${item.secretNames.join(' ') || '-'}`,
        `VOLUMES     ${item.networkVolumeMounts.map(mount => mount.mountPath).join(' ') || '-'}`,
        `PORTS       ${item.publicPorts.join(' ') || '-'}`,
      ];

      if (item.description) {
        lines.push('', item.description);
      }

      return { text: lines.join('\n') };
    });
  });
