import { z } from 'zod';

/** Mirrors the customer-facing `instanceDto` fields the CLI renders. */
export const instanceDtoSchema = z.object({
  id: z.string(),
  shortId: z.string(),
  name: z.string(),
  computeLabel: z.string(),
  computeType: z.enum(['gpu', 'cpu']),
  computeSku: z.string().optional(),
  gpuCount: z.number().int(),
  state: z.enum([
    'reserving',
    'queued',
    'starting',
    'running',
    'stopping',
    'stopped',
    'restarting',
    'completed',
    'failed',
    'deleting',
    'deleted',
  ]),
  startupStage: z.enum([
    'waiting_for_gpu',
    'waiting_for_cpu',
    'preparing_container',
    'downloading_image',
    'starting_container',
    'retrying_image_download',
  ]).nullable(),
  costMicrosPerHour: z.string(),
  ephemeralDiskGiB: z.number().int(),
  volumeDiskGiB: z.number().int().nullable().optional(),
  networkVolumeMounts: z.array(z.object({
    mountPath: z.string(),
    volume: z.object({
      id: z.string(),
      name: z.string(),
      capacityGiB: z.number().int(),
    }).passthrough(),
  })).optional(),
  publicPorts: z.array(z.object({ port: z.number().int(), url: z.string().nullable() })).optional(),
  resources: z.object({
    gpuModel: z.string().nullable(),
    gpuMemoryGiB: z.number().int().nullable(),
    cpuCores: z.number().int(),
    memoryGiB: z.number().int(),
  }).optional(),
  pricing: z.object({
    computeMicrosPerHour: z.string(),
    diskMicrosPerHour: z.string(),
    volumeDiskMicrosPerHour: z.string(),
    billingActive: z.boolean(),
    volumeDiskBillingActive: z.boolean(),
  }).optional(),
  runtimeStartedAt: z.string().nullable().optional(),
  templateVersion: z.object({
    id: z.string(),
    version: z.string(),
    name: z.string(),
    image: z.string(),
  }).nullable().optional(),
  image: z.object({
    source: z.enum(['template', 'custom']),
    reference: z.string(),
    requestedReference: z.string(),
    registryCredential: z.object({
      id: z.string(),
      name: z.string(),
      registry: z.string(),
    }).nullable(),
  }).optional(),
  command: z.array(z.string()).optional(),
  environment: z.array(z.object({ name: z.string(), value: z.string() })).optional(),
  secretNames: z.array(z.string()).optional(),
  lastError: z.string().nullable().optional(),
  ssh: z.object({
    host: z.string(),
    hasKeys: z.boolean(),
    command: z.string().nullable(),
    status: z.string(),
  }).optional(),
  createdAt: z.string(),
  updatedAt: z.string().optional(),
  deletedAt: z.string().nullable().optional(),
});

export type InstanceDto = z.infer<typeof instanceDtoSchema>;

export const paginationSchema = z.object({
  page: z.number().int(),
  pageSize: z.number().int(),
  totalItems: z.number().int(),
  totalPages: z.number().int(),
});

export const instanceListSchema = z.object({
  items: z.array(instanceDtoSchema),
  pagination: paginationSchema,
});

export type InstanceListResponse = z.infer<typeof instanceListSchema>;

/** `GET /instances/:id/history` item. */
export const instanceEventSchema = z.object({
  id: z.string(),
  type: z.enum(['Normal', 'Warning']),
  reason: z.string(),
  message: z.string(),
  firstObservedAt: z.string(),
  lastObservedAt: z.string(),
  count: z.number().int(),
  actor: z.object({
    id: z.string(),
    name: z.string(),
    email: z.string(),
  }).nullable(),
});

export type InstanceEvent = z.infer<typeof instanceEventSchema>;

/** `GET /me` (session or `users:read` key). */
export const meSchema = z.object({
  data: z.object({
    user: z.object({
      id: z.string(),
      name: z.string(),
      email: z.string(),
      emailVerified: z.boolean(),
      image: z.string().nullable(),
    }),
    organization: z.object({
      id: z.string(),
      name: z.string(),
      slug: z.string(),
    }),
    membership: z.object({
      id: z.string(),
      role: z.enum(['owner', 'admin', 'member']),
    }),
  }),
});

export type MeResponse = z.infer<typeof meSchema>;

/** `GET /organization` (unwrapped from `{ data }`). */
export const organizationSchema = z.object({
  organization: z.object({
    id: z.string(),
    name: z.string(),
    slug: z.string(),
  }),
  membership: z.object({
    id: z.string(),
    role: z.enum(['owner', 'admin', 'member']),
  }),
  gpuCount: z.number().int(),
  gpuLimit: z.number().int(),
  cpuCount: z.number().int(),
  cpuLimit: z.number().int(),
});

export type Organization = z.infer<typeof organizationSchema>;

/** `GET /me/ssh-keys` item (unwrapped from `{ data }`). */
export const sshKeySchema = z.object({
  id: z.string(),
  name: z.string(),
  fingerprint: z.string(),
  createdAt: z.string(),
});

export type SshKey = z.infer<typeof sshKeySchema>;

/** `GET /registry-credentials` item (unwrapped from `{ data }`). */
export const registryCredentialSchema = z.object({
  id: z.string(),
  name: z.string(),
  registry: z.string(),
});

export type RegistryCredential = z.infer<typeof registryCredentialSchema>;

/** `GET /billing` (requires a `users:read` key). */
export const billingSummarySchema = z.object({
  balanceMicros: z.string(),
  currency: z.string(),
  topUpPresetsMicros: z.array(z.string()),
  stripeEnabled: z.boolean(),
  meteringEnabled: z.boolean(),
  enforcementEnabled: z.boolean(),
  autoReload: z.object({
    enabled: z.boolean(),
    thresholdMicros: z.string(),
    amountMicros: z.string(),
    paymentMethodReady: z.boolean(),
    consentedAt: z.string().nullable(),
  }),
});

export type BillingSummary = z.infer<typeof billingSummarySchema>;

/** `GET /billing/ledger` item: one resource, one sku, one organization-local day. */
export const ledgerEntrySchema = z.object({
  id: z.string(),
  type: z.enum(['top_up', 'usage', 'adjustment', 'refund']),
  amountMicros: z.string(),
  description: z.string(),
  resourceKind: z.enum(['instance', 'job', 'network_storage']).nullable(),
  resourceName: z.string().nullable(),
  skuDisplayName: z.string().nullable(),
  unit: z.string().nullable(),
  quantity: z.string().nullable(),
  chargedAt: z.string(),
  chargedUntil: z.string(),
});

export const paginatedLedgerSchema = z.object({
  items: z.array(ledgerEntrySchema),
  pagination: z.object({
    page: z.number(),
    pageSize: z.number(),
    totalItems: z.number(),
    totalPages: z.number(),
  }),
});

export type LedgerEntry = z.infer<typeof ledgerEntrySchema>;
export type PaginatedLedger = z.infer<typeof paginatedLedgerSchema>;
