import { z } from 'zod';
import { paginationSchema } from './instance';

export const runJobStateSchema = z.enum([
  'queued',
  'starting',
  'running',
  'retrying',
  'cancelling',
  'succeeded',
  'failed',
  'cancelled',
  'timed_out',
  'interrupted',
  'deleting',
]);

export const runJobAttemptSchema = z.object({
  number: z.number().int(),
  state: runJobStateSchema,
  scheduledAt: z.string().nullable(),
  startedAt: z.string().nullable(),
  finishedAt: z.string().nullable(),
  exitCode: z.number().int().nullable(),
  containerRunId: z.string().nullable(),
});

export const runJobDtoSchema = z.object({
  id: z.string().uuid(),
  shortId: z.string(),
  name: z.string(),
  state: runJobStateSchema,
  admissionState: z.enum(['waiting', 'admitted']).optional(),
  admittedAt: z.string().nullable().optional(),
  computeType: z.enum(['gpu', 'cpu']),
  computeSku: z.string(),
  gpuCount: z.number().int(),
  resources: z.object({
    gpuModel: z.string().nullable(),
    gpuMemoryGiB: z.number().int().nullable(),
    cpuCores: z.number().int(),
    memoryGiB: z.number().int(),
  }),
  ephemeralDiskGiB: z.number().int(),
  command: z.array(z.string()),
  workingDirectory: z.string(),
  environment: z.array(z.object({ name: z.string(), value: z.string() })),
  timeoutSeconds: z.number().int(),
  preemptionRetries: z.number().int(),
  attemptNumber: z.number().int(),
  attempts: z.array(runJobAttemptSchema),
  retainLogs: z.boolean(),
  logsComplete: z.boolean(),
  logsTruncated: z.boolean(),
  exitCode: z.number().int().nullable(),
  statusReason: z.string().nullable(),
  statusMessage: z.string().nullable(),
  scheduledAt: z.string().nullable(),
  startedAt: z.string().nullable(),
  finishedAt: z.string().nullable(),
  costMicrosPerHour: z.string(),
  computeDiscount: z.object({ name: z.string(), listUnitPriceMicros: z.string(), unitPriceMicros: z.string() }).nullable().optional(),
  image: z.object({ source: z.enum(['template', 'custom']), reference: z.string() }),
  networkVolumeMounts: z.array(z.object({
    id: z.string().uuid(),
    name: z.string(),
    mountPath: z.string(),
  })),
  createdAt: z.string(),
  updatedAt: z.string(),
});

export const runJobListSchema = z.object({
  items: z.array(runJobDtoSchema),
  pagination: paginationSchema,
});

export const runJobLogsSchema = z.object({
  logs: z.string(),
  complete: z.boolean(),
  truncated: z.boolean(),
});

export const runJobLogHistorySchema = z.object({
  status: z.enum(['available', 'no_data']),
  text: z.string(),
  omittedBytes: z.number().int(),
});

export type RunJobDto = z.infer<typeof runJobDtoSchema>;
export type RunJobLogHistory = z.infer<typeof runJobLogHistorySchema>;
export type RunJobState = z.infer<typeof runJobStateSchema>;
export type RunJobListResponse = z.infer<typeof runJobListSchema>;
export type RunJobLogs = z.infer<typeof runJobLogsSchema>;
