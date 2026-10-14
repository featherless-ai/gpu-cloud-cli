import { z } from 'zod';

/** `NetworkVolumeService.volumeDto` output. */
export const networkVolumeDtoSchema = z.object({
  id: z.string(),
  name: z.string(),
  capacityGiB: z.number().int(),
  state: z.enum(['provisioning', 'available', 'resizing', 'error', 'deleting', 'deleted', 'in_use']),
  attachments: z.array(z.object({
    id: z.string(),
    name: z.string(),
    shortId: z.string(),
    gpuCount: z.number().int(),
    computeType: z.enum(['gpu', 'cpu']),
    cpuCores: z.number().int(),
    mountPath: z.string(),
  })),
  pricing: z.object({
    unitPriceMicrosPerGiBMonth: z.string(),
    estimatedMicrosPerMonth: z.string(),
    billingActive: z.boolean(),
  }),
  lastError: z.string().nullable(),
  createdAt: z.string(),
  updatedAt: z.string(),
});

export type NetworkVolumeDto = z.infer<typeof networkVolumeDtoSchema>;
