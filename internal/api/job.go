package api

type RunJobState string

const (
	JobQueued      RunJobState = "queued"
	JobStarting    RunJobState = "starting"
	JobRunning     RunJobState = "running"
	JobRetrying    RunJobState = "retrying"
	JobCancelling  RunJobState = "cancelling"
	JobSucceeded   RunJobState = "succeeded"
	JobFailed      RunJobState = "failed"
	JobCancelled   RunJobState = "cancelled"
	JobTimedOut    RunJobState = "timed_out"
	JobInterrupted RunJobState = "interrupted"
	JobDeleting    RunJobState = "deleting"
)

var _ = registerEnum(
	JobQueued, JobStarting, JobRunning, JobRetrying, JobCancelling, JobSucceeded,
	JobFailed, JobCancelled, JobTimedOut, JobInterrupted, JobDeleting,
)

type AdmissionState string

const (
	AdmissionWaiting  AdmissionState = "waiting"
	AdmissionAdmitted AdmissionState = "admitted"
)

var _ = registerEnum(AdmissionWaiting, AdmissionAdmitted)

type RunJobAttempt struct {
	Number         int         `json:"number"`
	State          RunJobState `json:"state"`
	ScheduledAt    *string     `json:"scheduledAt"`
	StartedAt      *string     `json:"startedAt"`
	FinishedAt     *string     `json:"finishedAt"`
	ExitCode       *int        `json:"exitCode"`
	ContainerRunID *string     `json:"containerRunId"`
}

type RunJob struct {
	ID                  string                   `json:"id" format:"uuid"`
	ShortID             string                   `json:"shortId"`
	Name                string                   `json:"name"`
	State               RunJobState              `json:"state"`
	AdmissionState      *AdmissionState          `json:"admissionState,omitzero"`
	AdmittedAt          Nullish[string]          `json:"admittedAt,omitzero"`
	ComputeType         ComputeType              `json:"computeType"`
	ComputeSKU          string                   `json:"computeSku"`
	GPUCount            int                      `json:"gpuCount"`
	Resources           Resources                `json:"resources"`
	EphemeralDiskGiB    int                      `json:"ephemeralDiskGiB"`
	Command             []string                 `json:"command"`
	WorkingDirectory    string                   `json:"workingDirectory"`
	Environment         []EnvVar                 `json:"environment"`
	TimeoutSeconds      int                      `json:"timeoutSeconds"`
	PreemptionRetries   int                      `json:"preemptionRetries"`
	AttemptNumber       int                      `json:"attemptNumber"`
	Attempts            []RunJobAttempt          `json:"attempts"`
	RetainLogs          bool                     `json:"retainLogs"`
	LogsComplete        bool                     `json:"logsComplete"`
	LogsTruncated       bool                     `json:"logsTruncated"`
	ExitCode            *int                     `json:"exitCode"`
	StatusReason        *string                  `json:"statusReason"`
	StatusMessage       *string                  `json:"statusMessage"`
	ScheduledAt         *string                  `json:"scheduledAt"`
	StartedAt           *string                  `json:"startedAt"`
	FinishedAt          *string                  `json:"finishedAt"`
	CostMicrosPerHour   string                   `json:"costMicrosPerHour"`
	ComputeDiscount     Nullish[ComputeDiscount] `json:"computeDiscount,omitzero"`
	Image               RunJobImage              `json:"image"`
	NetworkVolumeMounts []RunJobVolumeMount      `json:"networkVolumeMounts"`
	CreatedAt           string                   `json:"createdAt"`
	UpdatedAt           string                   `json:"updatedAt"`
}

type ComputeDiscount struct {
	Name                string `json:"name"`
	ListUnitPriceMicros string `json:"listUnitPriceMicros"`
	UnitPriceMicros     string `json:"unitPriceMicros"`
}

type RunJobImage struct {
	Source    ImageSource `json:"source"`
	Reference string      `json:"reference"`
}

type RunJobVolumeMount struct {
	ID        string `json:"id" format:"uuid"`
	Name      string `json:"name"`
	MountPath string `json:"mountPath"`
}

type RunJobList struct {
	Items      []RunJob   `json:"items"`
	Pagination Pagination `json:"pagination"`
}

type RunJobLogs struct {
	Logs      string `json:"logs"`
	Complete  bool   `json:"complete"`
	Truncated bool   `json:"truncated"`
}

type LogHistoryStatus string

const (
	LogHistoryAvailable LogHistoryStatus = "available"
	LogHistoryNoData    LogHistoryStatus = "no_data"
)

var _ = registerEnum(LogHistoryAvailable, LogHistoryNoData)

// RunJobLogHistory is one stored run's output.
type RunJobLogHistory struct {
	Status       LogHistoryStatus `json:"status"`
	Text         string           `json:"text"`
	OmittedBytes int              `json:"omittedBytes"`
}
