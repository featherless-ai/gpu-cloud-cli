package parity

// Responses of a small fake account: one GPU instance, one stopped CPU
// instance, a Run Job, a volume, two templates, keys and billing. Every
// fixture passes the TypeScript CLI's schemas, so both CLIs render them.

const instanceResearch = `{
  "id": "6f1c2d3e-4a5b-4c6d-8e7f-901a2b3c4d5e",
  "shortId": "rcsrvch012",
  "name": "research",
  "computeLabel": "1x MI325X",
  "computeType": "gpu",
  "computeSku": "instance.gpu.mi325x.1",
  "gpuCount": 1,
  "state": "running",
  "startupStage": null,
  "costMicrosPerHour": "2512345",
  "ephemeralDiskGiB": 200,
  "volumeDiskGiB": null,
  "networkVolumeMounts": [
    {"mountPath": "/workspace/data", "volume": {"id": "9a8b7c6d-5e4f-4a3b-9c2d-1e0f9a8b7c6d", "name": "datasets", "capacityGiB": 500, "state": "in_use"}}
  ],
  "publicPorts": [{"port": 8080, "url": "https://rcsrvch012-8080.example.test"}, {"port": 9000, "url": null}],
  "resources": {"gpuModel": "MI325X", "gpuMemoryGiB": 256, "cpuCores": 13, "memoryGiB": 224},
  "pricing": {"computeMicrosPerHour": "2500000", "diskMicrosPerHour": "12345", "volumeDiskMicrosPerHour": "0", "billingActive": true, "volumeDiskBillingActive": false},
  "runtimeStartedAt": "2026-11-05T08:00:00.000Z",
  "templateVersion": {"id": "1b2c3d4e-5f60-4718-9a2b-3c4d5e6f7081", "version": "2.4.0", "name": "PyTorch", "image": "featherlesscloud/pytorch:2.4.0"},
  "template": {"id": "0a1b2c3d-4e5f-4607-8819-2a3b4c5d6e7f", "shortId": "pytorch001", "name": "PyTorch"},
  "image": {"source": "template", "reference": "featherlesscloud/pytorch@sha256:abc", "requestedReference": "featherlesscloud/pytorch:2.4.0", "registryCredential": null},
  "command": ["sleep", "infinity"],
  "environment": [{"name": "HF_HOME", "value": "/workspace/data/hf"}],
  "secretNames": ["HF_TOKEN"],
  "lastError": null,
  "ssh": {"host": "ssh.example.test", "hasKeys": true, "command": "ssh rcsrvch012@ssh.example.test", "status": "ready"},
  "createdAt": "2026-11-05T07:58:00.000Z",
  "updatedAt": "2026-11-05T08:00:00.000Z",
  "deletedAt": null,
  "unknownFieldTheCliDrops": true
}`

const instanceIdle = `{
  "id": "7a2b3c4d-5e6f-4708-9a1b-2c3d4e5f6071",
  "shortId": "bx3cpq4567",
  "name": "idle box",
  "computeLabel": "8 vCPU",
  "computeType": "cpu",
  "gpuCount": 0,
  "state": "stopped",
  "startupStage": null,
  "costMicrosPerHour": "90000",
  "ephemeralDiskGiB": 50,
  "image": {"source": "custom", "reference": "ghcr.io/acme/tool:1", "requestedReference": "ghcr.io/acme/tool:1", "registryCredential": {"id": "4c5d6e7f-8091-4a2b-8c3d-4e5f60718293", "name": "ghcr", "registry": "ghcr.io"}},
  "createdAt": "2026-10-01T10:00:00.000Z"
}`

const instanceList = `{"items": [` + instanceResearch + `,` + instanceIdle + `], "pagination": {"page": 1, "pageSize": 100, "totalItems": 2, "totalPages": 1}}`

const instanceHistory = `[
  {"id": "e1", "type": "Normal", "reason": "Started", "message": "Instance is running", "firstObservedAt": "2026-11-05T08:00:00.000Z", "lastObservedAt": "2026-11-05T08:00:00.000Z", "count": 1, "actor": null},
  {"id": "e2", "type": "Warning", "reason": "Restarted", "message": "The container restarted 2 times", "firstObservedAt": "2026-11-05T09:00:00.000Z", "lastObservedAt": "2026-11-05T09:10:00.000Z", "count": 2, "actor": {"id": "u1", "name": "Dev", "email": "dev@example.test"}}
]`

const instanceLogs = `{"logs": "line one\nline two\n"}`

const me = `{"data": {"user": {"id": "u1", "name": "Dev", "email": "dev@example.test", "emailVerified": true, "image": null}, "organization": {"id": "o1", "name": "Acme", "slug": "acme"}, "membership": {"id": "m1", "role": "owner"}}}`

const organization = `{"data": {"organization": {"id": "o1", "name": "Acme", "slug": "acme"}, "membership": {"id": "m1", "role": "owner"}, "gpuCount": 1, "gpuLimit": 8, "cpuCount": 13, "cpuLimit": 128}}`

const pricing = `[
  {"sku": "instance.gpu.mi325x.1", "displayName": "MI325X", "resourceType": "instance.gpu", "unit": "gpu_hour", "gpuCount": 1, "unitPriceMicros": "2500000", "compareAtUnitPriceMicros": null, "currency": "USD", "effectiveAt": "2026-01-01T00:00:00.000Z", "expiresAt": null, "resources": {"gpuModel": "MI325X", "gpuMemoryGiB": 256, "cpuCores": 13, "memoryGiB": 224}},
  {"sku": "instance.gpu.mi325x.8", "displayName": "MI325X", "resourceType": "instance.gpu", "unit": "gpu_hour", "gpuCount": 8, "unitPriceMicros": "2400000", "compareAtUnitPriceMicros": "2500000", "currency": "USD", "effectiveAt": "2026-01-01T00:00:00.000Z", "expiresAt": null, "resources": {"gpuModel": "MI325X", "gpuMemoryGiB": 256, "cpuCores": 104, "memoryGiB": 1792}},
  {"sku": "instance.cpu.8", "displayName": "8 vCPU", "resourceType": "instance.cpu", "unit": "vcpu_hour", "gpuCount": null, "unitPriceMicros": "11250", "compareAtUnitPriceMicros": null, "currency": "USD", "effectiveAt": "2026-01-01T00:00:00.000Z", "expiresAt": null, "resources": {"gpuModel": null, "gpuMemoryGiB": null, "cpuCores": 8, "memoryGiB": 32}},
  {"sku": "storage.network", "displayName": "Network storage", "resourceType": "storage", "unit": "gib_month", "gpuCount": null, "unitPriceMicros": "70000", "compareAtUnitPriceMicros": null, "currency": "USD", "effectiveAt": "2026-01-01T00:00:00.000Z", "expiresAt": null}
]`

const runJobPricing = `[
  {"sku": "run-job.gpu.mi325x.1", "displayName": "MI325X", "resourceType": "run-job.gpu", "unit": "gpu_hour", "gpuCount": 1, "unitPriceMicros": "2000000", "compareAtUnitPriceMicros": null, "currency": "USD", "effectiveAt": "2026-01-01T00:00:00.000Z", "expiresAt": null, "resources": {"gpuModel": "MI325X", "gpuMemoryGiB": 256, "cpuCores": 13, "memoryGiB": 224}},
  {"sku": "run-job.cpu.4", "displayName": "4 vCPU", "resourceType": "run-job.cpu", "unit": "vcpu_hour", "gpuCount": null, "unitPriceMicros": "10000", "compareAtUnitPriceMicros": null, "currency": "USD", "effectiveAt": "2026-01-01T00:00:00.000Z", "expiresAt": "2027-01-01T00:00:00.000Z", "resources": {"gpuModel": null, "gpuMemoryGiB": null, "cpuCores": 4, "memoryGiB": 16}}
]`

const templatePyTorch = `{
  "id": "0a1b2c3d-4e5f-4607-8819-2a3b4c5d6e7f", "shortId": "pytorch001", "slug": "pytorch", "name": "PyTorch", "description": "PyTorch with ROCm",
  "imageRepository": "featherlesscloud/pytorch", "enabled": true, "computeTypes": ["gpu"],
  "version": {"id": "1b2c3d4e-5f60-4718-9a2b-3c4d5e6f7081", "version": "2.4.0", "tag": "2.4.0", "state": "published", "image": "featherlesscloud/pytorch:2.4.0", "startupCommand": "jupyter lab"},
  "official": true, "visibility": "public", "owner": null, "canEdit": false,
  "image": "featherlesscloud/pytorch:2.4.0", "imageSource": "template", "registryCredentialId": null, "computeSku": null, "ephemeralDiskGiB": null,
  "networkVolumeMounts": [], "command": [], "environment": [], "secretNames": [], "publicPorts": [8888], "autoTmux": null,
  "tags": ["training"], "gpuVendors": ["amd"],
  "versions": [{"version": "2.4.0", "image": "featherlesscloud/pytorch:2.4.0"}, {"version": "2.3.1", "image": "featherlesscloud/pytorch:2.3.1"}]
}`

const templateMine = `{
  "id": "2c3d4e5f-6071-4829-8a3b-4c5d6e7f8091", "shortId": "mytmpl0001", "slug": "my-trainer", "name": "My trainer", "description": "",
  "imageRepository": null, "enabled": true, "computeTypes": ["gpu", "cpu"],
  "version": {"id": "3d4e5f60-7182-4930-9b4c-5d6e7f809102", "version": "1", "tag": null, "state": "published", "image": "ghcr.io/acme/trainer:1", "startupCommand": ""},
  "official": false, "visibility": "organization", "owner": {"id": "u1", "name": "Dev"}, "canEdit": true,
  "image": "ghcr.io/acme/trainer:1", "imageSource": "custom", "registryCredentialId": "4c5d6e7f-8091-4a2b-8c3d-4e5f60718293", "computeSku": "instance.gpu.mi325x.1", "ephemeralDiskGiB": 100,
  "networkVolumeMounts": [{"networkVolumeId": "9a8b7c6d-5e4f-4a3b-9c2d-1e0f9a8b7c6d", "mountPath": "/data"}], "command": ["python", "train.py"],
  "environment": [{"name": "EPOCHS", "value": "3"}], "secretNames": [], "publicPorts": [], "autoTmux": "ssh-only"
}`

const templates = `[` + templatePyTorch + `,` + templateMine + `]`

const volumeDatasets = `{
  "id": "9a8b7c6d-5e4f-4a3b-9c2d-1e0f9a8b7c6d", "name": "datasets", "capacityGiB": 500, "state": "in_use",
  "attachments": [{"id": "6f1c2d3e-4a5b-4c6d-8e7f-901a2b3c4d5e", "name": "research", "shortId": "rcsrvch012", "gpuCount": 1, "computeType": "gpu", "cpuCores": 13, "mountPath": "/workspace/data"}],
  "pricing": {"unitPriceMicrosPerGiBMonth": "70000", "estimatedMicrosPerMonth": "35000000", "billingActive": true},
  "lastError": null, "createdAt": "2026-10-20T12:00:00.000Z", "updatedAt": "2026-11-05T08:00:00.000Z"
}`

const volumes = `[` + volumeDatasets + `]`

const runJob = `{
  "id": "5e6f7081-92a3-44b5-86c7-d8e9f0a1b2c3", "shortId": "jb7xk2mq9z", "name": "nightly-eval", "state": "succeeded",
  "admissionState": "admitted", "admittedAt": "2026-11-05T01:00:01.000Z",
  "computeType": "cpu", "computeSku": "run-job.cpu.4", "gpuCount": 0,
  "resources": {"gpuModel": null, "gpuMemoryGiB": null, "cpuCores": 4, "memoryGiB": 16},
  "ephemeralDiskGiB": 20, "command": ["python", "eval.py", "--all"], "workingDirectory": "/workspace",
  "environment": [{"name": "MODE", "value": "full"}], "timeoutSeconds": 86400, "preemptionRetries": 1,
  "attemptNumber": 2,
  "attempts": [
    {"number": 1, "state": "interrupted", "scheduledAt": "2026-11-05T01:00:01.000Z", "startedAt": "2026-11-05T01:00:20.000Z", "finishedAt": "2026-11-05T01:30:00.000Z", "exitCode": null, "containerRunId": "run-1"},
    {"number": 2, "state": "succeeded", "scheduledAt": "2026-11-05T01:30:05.000Z", "startedAt": "2026-11-05T01:30:30.000Z", "finishedAt": "2026-11-05T02:00:00.000Z", "exitCode": 0, "containerRunId": "run-2"}
  ],
  "retainLogs": true, "logsComplete": true, "logsTruncated": false, "exitCode": 0, "statusReason": null, "statusMessage": null,
  "scheduledAt": "2026-11-05T01:00:01.000Z", "startedAt": "2026-11-05T01:00:20.000Z", "finishedAt": "2026-11-05T02:00:00.000Z",
  "costMicrosPerHour": "40000", "computeDiscount": null,
  "image": {"source": "custom", "reference": "python:3.12"},
  "networkVolumeMounts": [{"id": "9a8b7c6d-5e4f-4a3b-9c2d-1e0f9a8b7c6d", "name": "datasets", "mountPath": "/workspace/data"}],
  "createdAt": "2026-11-05T01:00:00.000Z", "updatedAt": "2026-11-05T02:00:00.000Z"
}`

const runJobList = `{"items": [` + runJob + `], "pagination": {"page": 1, "pageSize": 20, "totalItems": 1, "totalPages": 1}}`

const runJobLogs = `{"logs": "evaluating\ndone\n", "complete": true, "truncated": false}`

const runJobLogHistory = `{"status": "available", "text": "first attempt output\n", "omittedBytes": 0}`

const sshKeys = `{"data": [{"id": "5f607182-93a4-45b6-87c8-d9e0f1a2b3c4", "name": "laptop", "fingerprint": "SHA256:abcdef", "createdAt": "2026-09-01T00:00:00.000Z"}]}`

const registryCredentials = `{"data": [{"id": "4c5d6e7f-8091-4a2b-8c3d-4e5f60718293", "name": "ghcr", "registry": "ghcr.io", "username": "acme-bot", "updatedAt": "2026-09-02T00:00:00.000Z"}]}`

const billing = `{"balanceMicros": "123456789", "currency": "USD", "topUpPresetsMicros": ["10000000", "50000000"], "stripeEnabled": true, "meteringEnabled": true, "enforcementEnabled": true,
  "autoReload": {"enabled": true, "thresholdMicros": "5000000", "amountMicros": "20000000", "paymentMethodReady": true, "consentedAt": "2026-08-01T00:00:00.000Z"}}`

const ledger = `{"items": [
  {"id": "l1", "type": "usage", "amountMicros": "-2512345", "description": "research", "resourceKind": "instance", "resourceName": "research", "skuDisplayName": "MI325X", "unit": "gpu_hour", "quantity": "1.0049", "chargedAt": "2026-11-05T00:00:00.000Z", "chargedUntil": "2026-11-06T00:00:00.000Z"},
  {"id": "l2", "type": "top_up", "amountMicros": "50000000", "description": "Top up", "resourceKind": null, "resourceName": null, "skuDisplayName": null, "unit": null, "quantity": null, "chargedAt": "2026-11-01T00:00:00.000Z", "chargedUntil": "2026-11-01T00:00:00.000Z"}
], "pagination": {"page": 1, "pageSize": 50, "totalItems": 2, "totalPages": 1}}`
