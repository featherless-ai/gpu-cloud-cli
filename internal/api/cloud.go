// Package api is the Featherless Cloud API client.
package api

import (
	"context"
	"net/http"
	"strings"

	"featherless.ai/featherless-cloud/internal/errs"
)

// Cloud is the API surface commands use; *Client implements it. Tests embed
// Cloud in a fake and override only the methods they need.
type Cloud interface {
	// APIBase is the API's base URL without a trailing slash.
	APIBase() string

	GetMe(ctx context.Context) (Me, error)
	ListInstances(ctx context.Context, page, limit int) (InstanceList, error)
	GetInstance(ctx context.Context, id string) (Instance, error)
	CreateInstance(ctx context.Context, body CreateInstanceBody, idempotencyKey string) (Instance, error)
	PatchInstance(ctx context.Context, id string, body UpdateInstanceBody) (Instance, error)
	InstanceAction(ctx context.Context, id string, action InstanceAction) (Instance, error)
	InstanceHistory(ctx context.Context, id string) ([]InstanceEvent, error)
	// InstanceLogs leaves the line count to the API when tailLines is 0.
	InstanceLogs(ctx context.Context, id string, tailLines int, section LogSection) (InstanceLogs, error)
	CreateTerminalSession(ctx context.Context, id string, columns, rows int, target TerminalTarget) (TerminalSession, error)
	Pricing(ctx context.Context, workload Workload) ([]PriceRate, error)
	Templates(ctx context.Context) ([]Template, error)
	// Template takes a UUID or short id.
	Template(ctx context.Context, id string) (Template, error)
	CreateTemplate(ctx context.Context, body TemplateBody) (Template, error)
	UpdateTemplate(ctx context.Context, id string, body TemplateBody) (Template, error)
	// DeleteTemplate returns the HTTP status.
	DeleteTemplate(ctx context.Context, id string) (int, error)
	Organization(ctx context.Context) (Organization, error)
	ListVolumes(ctx context.Context) ([]NetworkVolume, error)
	CreateVolume(ctx context.Context, body CreateVolumeBody, idempotencyKey string) (NetworkVolume, error)
	GetVolume(ctx context.Context, id string) (NetworkVolume, error)
	PatchVolume(ctx context.Context, id string, body UpdateVolumeBody) (NetworkVolume, error)
	CreateFileSession(ctx context.Context, volumeID string) (FileSession, error)
	// DeleteVolume returns the HTTP status; the 202 body is empty.
	DeleteVolume(ctx context.Context, id string, detach bool) (int, error)
	ListJobs(ctx context.Context, page, limit int) (RunJobList, error)
	GetJob(ctx context.Context, id string) (RunJob, error)
	CreateJob(ctx context.Context, body CreateRunJobBody, idempotencyKey string) (RunJob, error)
	// JobLogs leaves the line count to the API when tailLines is 0.
	JobLogs(ctx context.Context, id string, tailLines int, section LogSection) (RunJobLogs, error)
	// JobLogHistory is one stored run's output, as the logs history keeps it.
	JobLogHistory(ctx context.Context, id, containerRunID string, section LogSection) (RunJobLogHistory, error)
	CancelJob(ctx context.Context, id string) (RunJob, error)
	DeleteJob(ctx context.Context, id string) (RunJob, error)
	SSHKeys(ctx context.Context) ([]SSHKey, error)
	AddSSHKey(ctx context.Context, body AddSSHKeyBody) (SSHKey, error)
	// DeleteSSHKey returns the HTTP status.
	DeleteSSHKey(ctx context.Context, id string) (int, error)
	RegistryCredentials(ctx context.Context) ([]RegistryCredential, error)
	AddRegistryCredential(ctx context.Context, body RegistryCredentialBody) (RegistryCredential, error)
	UpdateRegistryCredential(ctx context.Context, id string, body UpdateRegistryCredentialBody) (RegistryCredential, error)
	// DeleteRegistryCredential returns the HTTP status.
	DeleteRegistryCredential(ctx context.Context, id string) (int, error)
	BillingSummary(ctx context.Context) (BillingSummary, error)
	BillingLedger(ctx context.Context) (PaginatedLedger, error)
}

type InstanceAction string

const (
	ActionStart   InstanceAction = "start"
	ActionStop    InstanceAction = "stop"
	ActionRestart InstanceAction = "restart"
	ActionDelete  InstanceAction = "delete"
)

type LogSection string

const (
	SectionHead LogSection = "head"
	SectionTail LogSection = "tail"
)

// Workload picks the price list; anything but run-job lists instance prices.
type Workload string

const (
	WorkloadInstance Workload = "instance"
	WorkloadRunJob   Workload = "run-job"
)

type CreateInstanceBody struct {
	Name string `json:"name"`
	// TemplateID fills in whatever the rest of the body leaves out.
	TemplateID *string `json:"templateId,omitzero"`
	// TemplateVersion is an official template's version; its current one when left out.
	TemplateVersion      *string              `json:"templateVersion,omitzero"`
	ContainerImage       *string              `json:"containerImage,omitzero"`
	RegistryCredentialID Nullish[string]      `json:"registryCredentialId,omitzero"`
	ComputeSKU           *string              `json:"computeSku,omitzero"`
	GPUCount             *int                 `json:"gpuCount,omitzero"`
	EphemeralDiskGiB     *int                 `json:"ephemeralDiskGiB,omitzero"`
	NetworkVolumeMounts  []NetworkVolumeMount `json:"networkVolumeMounts,omitzero"`
	Command              []string             `json:"command,omitzero"`
	Environment          []EnvVar             `json:"environment,omitzero"`
	Secrets              []EnvVar             `json:"secrets,omitzero"`
	PublicPorts          []int                `json:"publicPorts,omitzero"`
}

// UpdateInstanceBody sends only what is set; an empty, non-nil slice clears.
type UpdateInstanceBody struct {
	Name                 *string         `json:"name,omitzero"`
	PublicPorts          []int           `json:"publicPorts,omitzero"`
	ContainerImage       *string         `json:"containerImage,omitzero"`
	RegistryCredentialID Nullish[string] `json:"registryCredentialId,omitzero"`
	EphemeralDiskGiB     *int            `json:"ephemeralDiskGiB,omitzero"`
	Command              []string        `json:"command,omitzero"`
	Environment          []EnvVar        `json:"environment,omitzero"`
}

type AddSSHKeyBody struct {
	Name      string `json:"name"`
	PublicKey string `json:"publicKey"`
}

type RegistryCredentialBody struct {
	Name     string `json:"name"`
	Registry string `json:"registry"`
	Username string `json:"username"`
	Secret   string `json:"secret"`
}

type UpdateRegistryCredentialBody struct {
	Name     *string `json:"name,omitzero"`
	Registry *string `json:"registry,omitzero"`
	Username *string `json:"username,omitzero"`
	Secret   *string `json:"secret,omitzero"`
}

type CreateVolumeBody struct {
	Name        string `json:"name"`
	CapacityGiB int    `json:"capacityGiB"`
}

type UpdateVolumeBody struct {
	Name        *string `json:"name,omitzero"`
	CapacityGiB *int    `json:"capacityGiB,omitzero"`
}

type CreateRunJobBody struct {
	Name string `json:"name"`
	// TemplateID fills in whatever the rest of the body leaves out, its command included.
	TemplateID *string `json:"templateId,omitzero"`
	// TemplateVersion is an official template's version; its current one when left out.
	TemplateVersion      *string              `json:"templateVersion,omitzero"`
	ContainerImage       *string              `json:"containerImage,omitzero"`
	RegistryCredentialID Nullish[string]      `json:"registryCredentialId,omitzero"`
	ComputeSKU           *string              `json:"computeSku,omitzero"`
	GPUCount             *int                 `json:"gpuCount,omitzero"`
	EphemeralDiskGiB     *int                 `json:"ephemeralDiskGiB,omitzero"`
	NetworkVolumeMounts  []NetworkVolumeMount `json:"networkVolumeMounts,omitzero"`
	Command              []string             `json:"command,omitzero"`
	WorkingDirectory     *string              `json:"workingDirectory,omitzero"`
	Environment          []EnvVar             `json:"environment,omitzero"`
	Secrets              []EnvVar             `json:"secrets,omitzero"`
	TimeoutSeconds       *int                 `json:"timeoutSeconds,omitzero"`
	PreemptionRetries    *int                 `json:"preemptionRetries,omitzero"`
	RetainLogs           *bool                `json:"retainLogs,omitzero"`
}

type TemplateBody struct {
	Name        *string             `json:"name,omitzero"`
	Description *string             `json:"description,omitzero"`
	Visibility  *TemplateVisibility `json:"visibility,omitzero"`
	// BaseTemplateID is an official template to start from.
	BaseTemplateID *string `json:"baseTemplateId,omitzero"`
	// BaseTemplateVersion is which of its versions; the current one when left out.
	BaseTemplateVersion  *string              `json:"baseTemplateVersion,omitzero"`
	ContainerImage       *string              `json:"containerImage,omitzero"`
	RegistryCredentialID Nullish[string]      `json:"registryCredentialId,omitzero"`
	ComputeSKU           Nullish[string]      `json:"computeSku,omitzero"`
	EphemeralDiskGiB     Nullish[int]         `json:"ephemeralDiskGiB,omitzero"`
	NetworkVolumeMounts  []NetworkVolumeMount `json:"networkVolumeMounts,omitzero"`
	Command              []string             `json:"command,omitzero"`
	Environment          []EnvVar             `json:"environment,omitzero"`
	// Secrets replace the saved secrets.
	Secrets     []EnvVar `json:"secrets,omitzero"`
	PublicPorts []int    `json:"publicPorts,omitzero"`
	// Tags replace the saved tags.
	Tags []string `json:"tags,omitzero"`
}

// TerminalTarget opens the named tmux session (Tmux) or reattaches one by id
// (TmuxSessionID); set exactly one.
type TerminalTarget struct {
	Tmux          string
	TmuxSessionID string
}

type TerminalSession struct {
	Token  string `json:"token"`
	WSPath string `json:"wsPath"`
	// TmuxSession is the session the grant attaches to; nil for a plain shell.
	TmuxSession *TmuxSession `json:"tmuxSession,omitzero"`
}

type TmuxSession struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Windows  float64 `json:"windows"`
	Attached float64 `json:"attached"`
}

// FileSession is a short-lived token for a volume's files, and where to use it.
type FileSession struct {
	Token     string `json:"token"`
	ExpiresAt string `json:"expiresAt"`
	BaseURL   string `json:"baseUrl" format:"url"`
}

// ErrorFromStatus maps an HTTP failure to the CLI's error kinds.
func ErrorFromStatus(status int, body string) error {
	return errs.New("not implemented")
}

type Client struct {
	apiBase string
	apiKey  string
	http    *http.Client
}

var _ Cloud = (*Client)(nil)

func NewClient(apiBase, apiKey string) *Client {
	return &Client{apiBase: strings.TrimSuffix(apiBase, "/"), apiKey: apiKey, http: &http.Client{}}
}

func (c *Client) APIBase() string {
	return c.apiBase
}

func (c *Client) GetMe(ctx context.Context) (Me, error) {
	return Me{}, errs.New("not implemented")
}

func (c *Client) ListInstances(ctx context.Context, page, limit int) (InstanceList, error) {
	return InstanceList{}, errs.New("not implemented")
}

func (c *Client) GetInstance(ctx context.Context, id string) (Instance, error) {
	return Instance{}, errs.New("not implemented")
}

func (c *Client) CreateInstance(ctx context.Context, body CreateInstanceBody, idempotencyKey string) (Instance, error) {
	return Instance{}, errs.New("not implemented")
}

func (c *Client) PatchInstance(ctx context.Context, id string, body UpdateInstanceBody) (Instance, error) {
	return Instance{}, errs.New("not implemented")
}

func (c *Client) InstanceAction(ctx context.Context, id string, action InstanceAction) (Instance, error) {
	return Instance{}, errs.New("not implemented")
}

func (c *Client) InstanceHistory(ctx context.Context, id string) ([]InstanceEvent, error) {
	return nil, errs.New("not implemented")
}

func (c *Client) InstanceLogs(ctx context.Context, id string, tailLines int, section LogSection) (InstanceLogs, error) {
	return InstanceLogs{}, errs.New("not implemented")
}

func (c *Client) CreateTerminalSession(ctx context.Context, id string, columns, rows int, target TerminalTarget) (TerminalSession, error) {
	return TerminalSession{}, errs.New("not implemented")
}

func (c *Client) Pricing(ctx context.Context, workload Workload) ([]PriceRate, error) {
	return nil, errs.New("not implemented")
}

func (c *Client) Templates(ctx context.Context) ([]Template, error) {
	return nil, errs.New("not implemented")
}

func (c *Client) Template(ctx context.Context, id string) (Template, error) {
	return Template{}, errs.New("not implemented")
}

func (c *Client) CreateTemplate(ctx context.Context, body TemplateBody) (Template, error) {
	return Template{}, errs.New("not implemented")
}

func (c *Client) UpdateTemplate(ctx context.Context, id string, body TemplateBody) (Template, error) {
	return Template{}, errs.New("not implemented")
}

func (c *Client) DeleteTemplate(ctx context.Context, id string) (int, error) {
	return 0, errs.New("not implemented")
}

func (c *Client) Organization(ctx context.Context) (Organization, error) {
	return Organization{}, errs.New("not implemented")
}

func (c *Client) ListVolumes(ctx context.Context) ([]NetworkVolume, error) {
	return nil, errs.New("not implemented")
}

func (c *Client) CreateVolume(ctx context.Context, body CreateVolumeBody, idempotencyKey string) (NetworkVolume, error) {
	return NetworkVolume{}, errs.New("not implemented")
}

func (c *Client) GetVolume(ctx context.Context, id string) (NetworkVolume, error) {
	return NetworkVolume{}, errs.New("not implemented")
}

func (c *Client) PatchVolume(ctx context.Context, id string, body UpdateVolumeBody) (NetworkVolume, error) {
	return NetworkVolume{}, errs.New("not implemented")
}

func (c *Client) CreateFileSession(ctx context.Context, volumeID string) (FileSession, error) {
	return FileSession{}, errs.New("not implemented")
}

func (c *Client) DeleteVolume(ctx context.Context, id string, detach bool) (int, error) {
	return 0, errs.New("not implemented")
}

func (c *Client) ListJobs(ctx context.Context, page, limit int) (RunJobList, error) {
	return RunJobList{}, errs.New("not implemented")
}

func (c *Client) GetJob(ctx context.Context, id string) (RunJob, error) {
	return RunJob{}, errs.New("not implemented")
}

func (c *Client) CreateJob(ctx context.Context, body CreateRunJobBody, idempotencyKey string) (RunJob, error) {
	return RunJob{}, errs.New("not implemented")
}

func (c *Client) JobLogs(ctx context.Context, id string, tailLines int, section LogSection) (RunJobLogs, error) {
	return RunJobLogs{}, errs.New("not implemented")
}

func (c *Client) JobLogHistory(ctx context.Context, id, containerRunID string, section LogSection) (RunJobLogHistory, error) {
	return RunJobLogHistory{}, errs.New("not implemented")
}

func (c *Client) CancelJob(ctx context.Context, id string) (RunJob, error) {
	return RunJob{}, errs.New("not implemented")
}

func (c *Client) DeleteJob(ctx context.Context, id string) (RunJob, error) {
	return RunJob{}, errs.New("not implemented")
}

func (c *Client) SSHKeys(ctx context.Context) ([]SSHKey, error) {
	return nil, errs.New("not implemented")
}

func (c *Client) AddSSHKey(ctx context.Context, body AddSSHKeyBody) (SSHKey, error) {
	return SSHKey{}, errs.New("not implemented")
}

func (c *Client) DeleteSSHKey(ctx context.Context, id string) (int, error) {
	return 0, errs.New("not implemented")
}

func (c *Client) RegistryCredentials(ctx context.Context) ([]RegistryCredential, error) {
	return nil, errs.New("not implemented")
}

func (c *Client) AddRegistryCredential(ctx context.Context, body RegistryCredentialBody) (RegistryCredential, error) {
	return RegistryCredential{}, errs.New("not implemented")
}

func (c *Client) UpdateRegistryCredential(ctx context.Context, id string, body UpdateRegistryCredentialBody) (RegistryCredential, error) {
	return RegistryCredential{}, errs.New("not implemented")
}

func (c *Client) DeleteRegistryCredential(ctx context.Context, id string) (int, error) {
	return 0, errs.New("not implemented")
}

func (c *Client) BillingSummary(ctx context.Context) (BillingSummary, error) {
	return BillingSummary{}, errs.New("not implemented")
}

func (c *Client) BillingLedger(ctx context.Context) (PaginatedLedger, error) {
	return PaginatedLedger{}, errs.New("not implemented")
}
