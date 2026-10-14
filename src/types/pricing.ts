import { z } from 'zod';

/** `GET /instances/pricing` entry (`computePriceSchema`). */
export const priceRateSchema = z.object({
  sku: z.string(),
  displayName: z.string(),
  resourceType: z.enum(['instance.gpu', 'instance.cpu', 'run-job.gpu', 'run-job.cpu', 'storage', 'bandwidth']),
  unit: z.enum(['gpu_hour', 'vcpu_hour', 'gib_month', 'gib']),
  gpuCount: z.number().int().nullable(),
  unitPriceMicros: z.string(),
  compareAtUnitPriceMicros: z.string().nullable(),
  currency: z.string(),
  effectiveAt: z.string(),
  expiresAt: z.string().nullable(),
  resources: z.object({
    gpuModel: z.string().nullable(),
    gpuMemoryGiB: z.number().int().nullable(),
    cpuCores: z.number().int(),
    memoryGiB: z.number().int(),
  }).optional(),
});

export type PriceRate = z.infer<typeof priceRateSchema>;

/** `GET /templates` item: template fields plus the single published `version`. */
export const catalogTemplateSchema = z.object({
  id: z.string(),
  name: z.string(),
  description: z.string(),
  imageRepository: z.string().nullable(),
  enabled: z.boolean(),
  computeTypes: z.array(z.enum(['gpu', 'cpu'])),
  version: z.object({
    id: z.string(),
    version: z.string(),
    tag: z.string().nullable(),
    state: z.string(),
    image: z.string(),
    startupCommand: z.string(),
  }),
});

export type CatalogTemplate = z.infer<typeof catalogTemplateSchema>;

/** Which kind of compute a rate sells, whichever workload it is sold to; null for storage and bandwidth. */
export function computeTypeOf(rate: Pick<PriceRate, 'resourceType'>): 'gpu' | 'cpu' | null {
  if (rate.resourceType.endsWith('.gpu')) {
    return 'gpu';
  }

  if (rate.resourceType.endsWith('.cpu')) {
    return 'cpu';
  }

  return null;
}
