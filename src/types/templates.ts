import { z } from 'zod';
import { catalogTemplateSchema } from './pricing';

/** What an owner may set on their own template; official templates are public. */
export const TEMPLATE_VISIBILITIES = ['private', 'organization'] as const;
export type TemplateVisibility = typeof TEMPLATE_VISIBILITIES[number];

/** `GET /templates` item: an official template, your own, or one shared in your organization. */
export const templateSchema = catalogTemplateSchema.extend({
  shortId: z.string(),
  slug: z.string(),
  official: z.boolean(),
  visibility: z.enum(['private', 'organization', 'public']),
  owner: z.object({ id: z.string(), name: z.string() }).nullable(),
  canEdit: z.boolean(),
  image: z.string(),
  imageSource: z.enum(['template', 'custom']),
  registryCredentialId: z.string().nullable(),
  computeSku: z.string().nullable(),
  ephemeralDiskGiB: z.number().int().nullable(),
  networkVolumeMounts: z.array(z.object({ networkVolumeId: z.string(), mountPath: z.string() })),
  command: z.array(z.string()),
  environment: z.array(z.object({ name: z.string(), value: z.string() })),
  secretNames: z.array(z.string()),
  publicPorts: z.array(z.number().int()),
  autoTmux: z.enum(['off', 'ssh-only']).nullable(),
  tags: z.array(z.string()).default([]),
  /** An official template's versions, current first. */
  versions: z.array(z.object({ version: z.string(), image: z.string() })).default([]),
});

export type Template = z.infer<typeof templateSchema>;
