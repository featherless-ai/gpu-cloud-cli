import { z } from 'zod';
import {
  CliError, ConflictError, ForbiddenError, NotFoundError, RateLimitError, RetryableError, ServiceUnavailableError, UnauthorizedError,
} from '../error';
import { consoleBaseUrl } from '../config';
import {
  billingSummarySchema,
  instanceEventSchema,
  instanceListSchema,
  paginatedLedgerSchema,
  meSchema,
  organizationSchema,
  registryCredentialSchema,
  sshKeySchema,
  type BillingSummary,
  type InstanceDto,
  type InstanceEvent,
  type InstanceListResponse,
  type PaginatedLedger,
  type MeResponse,
  type Organization,
  type RegistryCredential,
  type SshKey,
  instanceDtoSchema,
} from '../types/instance';
import { networkVolumeDtoSchema, type NetworkVolumeDto } from '../types/volume';
import { catalogTemplateSchema, priceRateSchema, type CatalogTemplate, type PriceRate } from '../types/pricing';
import {
  runJobDtoSchema,
  runJobListSchema,
  runJobLogHistorySchema,
  runJobLogsSchema,
  type RunJobDto,
  type RunJobListResponse,
  type RunJobLogHistory,
  type RunJobLogs,
} from '../types/job';
import { VERSION } from '../version';

interface ApiErrorEnvelope {
  error?: {
    code?: string;
    message?: string;
    fields?: Array<{ field?: string; message?: string }>;
  };
}

interface Envelope<T> {
  data: T;
}

interface TypedRequestOptions {
  headers?: Record<string, string>;
  unwrapData?: boolean;
}

/** Maps an HTTP failure to the CLI error hierarchy. Test seam for CloudClient. */
export function errorFromStatus(status: number, bodyText: string): CliError {
  let envelope: ApiErrorEnvelope = {};
  let message: string | undefined;

  try {
    envelope = JSON.parse(bodyText) as ApiErrorEnvelope;
    message = envelope.error?.message;
  } catch {
    message = bodyText.trim().slice(0, 200) || undefined;
  }

  if (status === 401) {
    return new UnauthorizedError(message ?? 'Unauthorized — your API key may have been revoked. Run featherless-cloud login.');
  }

  if (status === 403) {
    return new ForbiddenError(message ?? 'Forbidden: the API key lacks the required scope.');
  }

  if (status === 404) {
    return new NotFoundError(message ?? 'Not found');
  }

  if (status === 409) {
    const code = envelope.error?.code ?? 'conflict';
    let conflict = message ?? 'Conflict';

    if (code === 'insufficient_credit') {
      conflict += ` Run featherless-cloud billing settings, or add credit at ${consoleBaseUrl()}/billing.`;
    }

    return new ConflictError(code, conflict);
  }

  if (status === 422) {
    const fields = (envelope.error?.fields ?? [])
      .filter(field => field.field && field.message)
      .map(field => `${field.field}: ${field.message}`);

    const base = message ?? 'The request was rejected.';

    return new CliError(fields.length ? `${base}\n${fields.join('\n')}` : base);
  }

  if (status === 429) {
    return new RateLimitError(message);
  }

  if (status >= 500) {
    return new ServiceUnavailableError(`API error ${status}: ${message ?? status}`);
  }

  return new CliError(`API error ${status}: ${message ?? status}`);
}

export interface CreateInstanceBody {
  name: string;
  templateVersionId?: string;
  containerImage?: string;
  registryCredentialId?: string | null;
  computeSku?: string;
  gpuCount?: number;
  ephemeralDiskGiB?: number;
  networkVolumeMounts?: Array<{ networkVolumeId: string; mountPath: string }>;
  command?: string[];
  environment?: Array<{ name: string; value: string }>;
  secrets?: Array<{ name: string; value: string }>;
  publicPorts?: number[];
}

export interface UpdateInstanceBody {
  name?: string;
  publicPorts?: number[];
  containerImage?: string;
  registryCredentialId?: string | null;
  ephemeralDiskGiB?: number;
  command?: string[];
  environment?: Array<{ name: string; value: string }>;
}

export interface AddSshKeyBody {
  name: string;
  publicKey: string;
}

export interface RegistryCredentialBody {
  name: string;
  registry: string;
  username: string;
  secret: string;
}

export type InstanceAction = 'start' | 'stop' | 'restart' | 'delete';

export interface CreateVolumeBody {
  name: string;
  capacityGiB: number;
}

export interface UpdateVolumeBody {
  name?: string;
  capacityGiB?: number;
}

export interface CreateRunJobBody {
  name: string;
  templateVersionId?: string;
  containerImage?: string;
  registryCredentialId?: string | null;
  computeSku?: string;
  gpuCount?: number;
  ephemeralDiskGiB?: number;
  networkVolumeMounts?: Array<{ networkVolumeId: string; mountPath: string }>;
  command: string[];
  workingDirectory?: string;
  environment?: Array<{ name: string; value: string }>;
  secrets?: Array<{ name: string; value: string }>;
  timeoutSeconds?: number;
  preemptionRetries?: number;
  retainLogs?: boolean;
}

export interface TerminalSession {
  token: string;
  wsPath: string;
  /** The session the grant attaches to; absent for a plain shell. */
  tmuxSession?: { id: string; name: string };
}

const fileSessionSchema = z.object({ token: z.string(), expiresAt: z.string(), baseUrl: z.string().url() });

/** A short-lived token for a volume's files, and where to use it. */
export type FileSession = z.infer<typeof fileSessionSchema>;

export class CloudClient {
  private readonly apiBase: string;

  constructor(base: string, private readonly apiKey: string) {
    this.apiBase = base.replace(/\/$/, '');
  }

  async getMe(): Promise<MeResponse> {
    const body = await this.requestJson('/me');
    const parsed = meSchema.safeParse(body);

    if (!parsed.success) {
      throw new CliError('Unexpected response from the Featherless API.');
    }

    return parsed.data;
  }

  async listInstances(opts: { page: number; limit: number }): Promise<InstanceListResponse> {
    const body = await this.requestJson(
      `/instances?page=${opts.page}&limit=${opts.limit}`,
    );

    const parsed = instanceListSchema.safeParse(body);

    if (!parsed.success) {
      throw new CliError('Unexpected response from the Featherless API.');
    }

    return parsed.data;
  }

  async getInstance(id: string): Promise<InstanceDto> {
    return this.typed('GET', `/instances/${encodeURIComponent(id)}`, undefined, instanceDtoSchema);
  }

  async createInstance(body: CreateInstanceBody, idempotencyKey: string): Promise<InstanceDto> {
    return this.typed('POST', '/instances/', body, instanceDtoSchema, {
      headers: { 'Idempotency-Key': idempotencyKey },
    });
  }

  async patchInstance(id: string, body: UpdateInstanceBody): Promise<InstanceDto> {
    return this.typed('PATCH', `/instances/${encodeURIComponent(id)}`, body, instanceDtoSchema);
  }

  async instanceAction(id: string, action: InstanceAction): Promise<InstanceDto> {
    return this.typed('POST', `/instances/${encodeURIComponent(id)}/${action}`, undefined, instanceDtoSchema);
  }

  async instanceHistory(id: string): Promise<InstanceEvent[]> {
    return this.typed('GET', `/instances/${encodeURIComponent(id)}/history`, undefined, z.array(instanceEventSchema));
  }

  async instanceLogs(id: string, tailLines?: number, section: 'head' | 'tail' = 'tail'): Promise<{ logs: string }> {
    const query = `?section=${section}${tailLines ? `&tailLines=${tailLines}` : ''}`;

    return this.typed('GET', `/instances/${encodeURIComponent(id)}/logs${query}`, undefined, z.object({ logs: z.string() }));
  }

  async createTerminalSession(
    id: string,
    columns: number,
    rows: number,
    target: { tmux: string } | { tmuxSessionId: string },
  ): Promise<TerminalSession> {
    return this.typed(
      'POST',
      `/instances/${encodeURIComponent(id)}/terminal/sessions`,
      { columns, rows, ...target },
      z.object({
        token: z.string(),
        wsPath: z.string(),
        tmuxSession: z.object({ id: z.string(), name: z.string(), windows: z.number(), attached: z.number() }).optional(),
      }),
    );
  }

  async pricing(workload: 'instance' | 'run-job' = 'instance'): Promise<PriceRate[]> {
    return this.typed('GET', workload === 'run-job' ? '/run-jobs/pricing' : '/instances/pricing', undefined, z.array(priceRateSchema));
  }

  async templates(): Promise<CatalogTemplate[]> {
    return this.typed('GET', '/templates/', undefined, z.array(catalogTemplateSchema));
  }

  async organization(): Promise<Organization> {
    return this.typed('GET', '/organization', undefined, organizationSchema, { unwrapData: true });
  }

  async listVolumes(): Promise<NetworkVolumeDto[]> {
    return this.typed('GET', '/volumes/', undefined, z.array(networkVolumeDtoSchema));
  }

  async createVolume(body: CreateVolumeBody, idempotencyKey: string): Promise<NetworkVolumeDto> {
    return this.typed('POST', '/volumes/', body, networkVolumeDtoSchema, {
      headers: { 'Idempotency-Key': idempotencyKey },
    });
  }

  async getVolume(id: string): Promise<NetworkVolumeDto> {
    return this.typed('GET', `/volumes/${encodeURIComponent(id)}`, undefined, networkVolumeDtoSchema);
  }

  async patchVolume(id: string, body: UpdateVolumeBody): Promise<NetworkVolumeDto> {
    return this.typed('PATCH', `/volumes/${encodeURIComponent(id)}`, body, networkVolumeDtoSchema);
  }

  async createFileSession(volumeId: string): Promise<FileSession> {
    return this.typed('POST', `/volumes/${encodeURIComponent(volumeId)}/files/session`, undefined, fileSessionSchema);
  }

  /** Returns the HTTP status; the 202 body is empty. */
  async deleteVolume(id: string, detach = false): Promise<number> {
    return this.requestWithStatus('DELETE', `/volumes/${encodeURIComponent(id)}${detach ? '?detach=true' : ''}`);
  }

  async listJobs(opts: { page: number; limit: number }): Promise<RunJobListResponse> {
    return this.typed('GET', `/run-jobs?page=${opts.page}&limit=${opts.limit}`, undefined, runJobListSchema);
  }

  async getJob(id: string): Promise<RunJobDto> {
    return this.typed('GET', `/run-jobs/${encodeURIComponent(id)}`, undefined, runJobDtoSchema);
  }

  async createJob(body: CreateRunJobBody, idempotencyKey: string): Promise<RunJobDto> {
    return this.typed('POST', '/run-jobs/', body, runJobDtoSchema, {
      headers: { 'Idempotency-Key': idempotencyKey },
    });
  }

  async jobLogs(id: string, tailLines?: number, section: 'head' | 'tail' = 'tail'): Promise<RunJobLogs> {
    const query = `?section=${section}${tailLines ? `&tailLines=${tailLines}` : ''}`;
    return this.typed('GET', `/run-jobs/${encodeURIComponent(id)}/logs${query}`, undefined, runJobLogsSchema);
  }

  /** One stored run's output, as the logs history keeps it. */
  async jobLogHistory(id: string, containerRunId: string, section: 'head' | 'tail' = 'tail'): Promise<RunJobLogHistory> {
    const query = new URLSearchParams({ containerRunId, section });
    return this.typed('GET', `/run-jobs/${encodeURIComponent(id)}/logs/history?${query}`, undefined, runJobLogHistorySchema);
  }

  async cancelJob(id: string): Promise<RunJobDto> {
    return this.typed('POST', `/run-jobs/${encodeURIComponent(id)}/cancel`, undefined, runJobDtoSchema);
  }

  async deleteJob(id: string): Promise<RunJobDto> {
    return this.typed('DELETE', `/run-jobs/${encodeURIComponent(id)}`, undefined, runJobDtoSchema);
  }

  async sshKeys(): Promise<SshKey[]> {
    return this.typed('GET', '/me/ssh-keys', undefined, z.array(sshKeySchema), { unwrapData: true });
  }

  async addSshKey(body: AddSshKeyBody): Promise<SshKey> {
    return this.typed('POST', '/me/ssh-keys', body, sshKeySchema, { unwrapData: true });
  }

  async deleteSshKey(id: string): Promise<number> {
    return this.requestWithStatus('DELETE', `/me/ssh-keys/${encodeURIComponent(id)}`);
  }

  async registryCredentials(): Promise<RegistryCredential[]> {
    return this.typed('GET', '/registry-credentials', undefined, z.array(registryCredentialSchema), { unwrapData: true });
  }

  async addRegistryCredential(body: RegistryCredentialBody): Promise<RegistryCredential> {
    return this.typed('POST', '/registry-credentials', body, registryCredentialSchema, { unwrapData: true });
  }

  async updateRegistryCredential(id: string, body: Partial<RegistryCredentialBody>): Promise<RegistryCredential> {
    return this.typed('PATCH', `/registry-credentials/${encodeURIComponent(id)}`, body, registryCredentialSchema, { unwrapData: true });
  }

  async deleteRegistryCredential(id: string): Promise<number> {
    return this.requestWithStatus('DELETE', `/registry-credentials/${encodeURIComponent(id)}`);
  }

  async billingSummary(): Promise<BillingSummary> {
    return this.typed('GET', '/billing', undefined, billingSummarySchema);
  }

  async billingLedger(): Promise<PaginatedLedger> {
    return this.typed('GET', '/billing/ledger', undefined, paginatedLedgerSchema);
  }

  private async typed<S extends z.ZodType>(
    method: string,
    path: string,
    body: unknown,
    schema: S,
    options: TypedRequestOptions = {},
  ): Promise<z.infer<S>> {
    const raw = await this.requestJson(path, {
      method,
      body: body === undefined ? undefined : JSON.stringify(body),
      headers: options.headers,
    });

    const parsed = schema.safeParse(options.unwrapData ? (raw as Envelope<unknown>).data : raw);

    if (!parsed.success) {
      throw new CliError('Unexpected response from the Featherless API.');
    }

    return parsed.data;
  }

  private async requestWithStatus(method: string, path: string): Promise<number> {
    const response = await this.send(path, { method });

    if (!response.ok) {
      throw errorFromStatus(response.status, await response.text().catch(() => ''));
    }

    await response.body?.cancel();

    return response.status;
  }

  private async requestJson(path: string, options: RequestInit = {}): Promise<unknown> {
    const response = await this.send(path, options);

    if (!response.ok) {
      throw errorFromStatus(response.status, await response.text().catch(() => ''));
    }

    return response.json();
  }

  private async send(path: string, options: RequestInit = {}): Promise<Response> {
    const headers = new Headers(options.headers);
    headers.set('Authorization', `Bearer ${this.apiKey}`);
    headers.set('User-Agent', `featherless-cloud/${VERSION}`);

    if (options.body && !headers.has('content-type')) {
      headers.set('content-type', 'application/json');
    }

    let response: Response;

    try {
      response = await fetch(`${this.apiBase}${path}`, { ...options, headers });
    } catch {
      throw new RetryableError(`Could not connect to ${this.apiBase}`);
    }

    return response;
  }
}
