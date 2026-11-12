// Package api is the Featherless Cloud API client.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf16"

	"featherless.ai/featherless-cloud/internal/config"
	"featherless.ai/featherless-cloud/internal/errs"
	"featherless.ai/featherless-cloud/internal/version"
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
	var envelope any
	var apiError map[string]any
	var message *string

	err := json.Unmarshal([]byte(body), &envelope)

	// A JSON null body falls back to the text too: reading its error throws.
	if err != nil || envelope == nil {
		text := firstUTF16Units(strings.TrimFunc(body, isJSSpace), 200)

		if text != "" {
			message = &text
		}
	} else {
		fields, _ := envelope.(map[string]any)
		apiError, _ = fields["error"].(map[string]any)
		message = jsText(apiError["message"])
	}

	switch {
	case status == 401:
		text := orText(message, "Unauthorized — your API key may have been revoked. Run featherless-cloud login.")
		return &errs.Error{Kind: errs.KindUnauthorized, Message: text}
	case status == 403:
		text := orText(message, "Forbidden: the API key lacks the required scope.")
		return &errs.Error{Kind: errs.KindForbidden, Message: text}
	case status == 404:
		return &errs.Error{Kind: errs.KindNotFound, Message: orText(message, "Not found")}
	case status == 409:
		code := orText(jsText(apiError["code"]), "conflict")
		conflict := orText(message, "Conflict")

		if code == "insufficient_credit" {
			conflict += " Run featherless-cloud billing settings, or add credit at " + config.ConsoleBaseURL() + "/billing."
		}

		return &errs.Error{Kind: errs.KindConflict, Message: conflict, Code: code}
	case status == 422:
		entries, _ := apiError["fields"].([]any)
		var lines []string

		for _, entry := range entries {
			field, _ := entry.(map[string]any)
			isComplete := jsTruthy(field["field"]) && jsTruthy(field["message"])

			if isComplete {
				lines = append(lines, *jsText(field["field"])+": "+*jsText(field["message"]))
			}
		}

		text := orText(message, "The request was rejected.")

		if len(lines) > 0 {
			text += "\n" + strings.Join(lines, "\n")
		}

		return &errs.Error{Kind: errs.KindCLI, Message: text}
	case status == 429:
		return &errs.Error{Kind: errs.KindRateLimit, Message: orText(message, "Rate limited")}
	}

	text := fmt.Sprintf("API error %d: %s", status, orText(message, strconv.Itoa(status)))

	if status >= 500 {
		return &errs.Error{Kind: errs.KindServiceUnavailable, Message: text}
	}

	return &errs.Error{Kind: errs.KindCLI, Message: text}
}

func orText(text *string, fallback string) string {
	if text == nil {
		return fallback
	}

	return *text
}

// jsText is String(value) for a parsed JSON value; nil for null and absent.
func jsText(value any) *string {
	var text string

	switch typed := value.(type) {
	case nil:
		return nil
	case string:
		text = typed
	case bool:
		text = strconv.FormatBool(typed)
	case float64:
		number := typed

		// String(-0) is "0".
		if number == 0 {
			number = 0
		}

		encoded, _ := json.Marshal(number)
		text = string(encoded)
	case []any:
		parts := make([]string, len(typed))

		for i, item := range typed {
			parts[i] = orText(jsText(item), "")
		}

		text = strings.Join(parts, ",")
	default:
		text = "[object Object]"
	}

	return &text
}

func jsTruthy(value any) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case bool:
		return typed
	case string:
		return typed != ""
	case float64:
		return typed != 0
	}

	return true
}

// firstUTF16Units is text.slice(0, limit), never splitting a surrogate pair.
func firstUTF16Units(text string, limit int) string {
	units := 0

	for index, r := range text {
		units += utf16.RuneLen(r)

		if units > limit {
			return text[:index]
		}
	}

	return text
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
	return typed[Me](ctx, c, call{method: http.MethodGet, path: "/me"})
}

func (c *Client) ListInstances(ctx context.Context, page, limit int) (InstanceList, error) {
	path := "/instances?page=" + strconv.Itoa(page) + "&limit=" + strconv.Itoa(limit)
	return typed[InstanceList](ctx, c, call{method: http.MethodGet, path: path})
}

func (c *Client) GetInstance(ctx context.Context, id string) (Instance, error) {
	return typed[Instance](ctx, c, call{method: http.MethodGet, path: "/instances/" + encodeURIComponent(id)})
}

func (c *Client) CreateInstance(ctx context.Context, body CreateInstanceBody, idempotencyKey string) (Instance, error) {
	request := call{method: http.MethodPost, path: "/instances/", body: body, idempotencyKey: idempotencyKey}
	return typed[Instance](ctx, c, request)
}

func (c *Client) PatchInstance(ctx context.Context, id string, body UpdateInstanceBody) (Instance, error) {
	request := call{method: http.MethodPatch, path: "/instances/" + encodeURIComponent(id), body: body}
	return typed[Instance](ctx, c, request)
}

func (c *Client) InstanceAction(ctx context.Context, id string, action InstanceAction) (Instance, error) {
	path := "/instances/" + encodeURIComponent(id) + "/" + string(action)
	return typed[Instance](ctx, c, call{method: http.MethodPost, path: path})
}

func (c *Client) InstanceHistory(ctx context.Context, id string) ([]InstanceEvent, error) {
	path := "/instances/" + encodeURIComponent(id) + "/history"
	return typed[[]InstanceEvent](ctx, c, call{method: http.MethodGet, path: path})
}

func (c *Client) InstanceLogs(ctx context.Context, id string, tailLines int, section LogSection) (InstanceLogs, error) {
	path := "/instances/" + encodeURIComponent(id) + "/logs" + logsQuery(tailLines, section)
	return typed[InstanceLogs](ctx, c, call{method: http.MethodGet, path: path})
}

func (c *Client) CreateTerminalSession(ctx context.Context, id string, columns, rows int, target TerminalTarget) (TerminalSession, error) {
	body := struct {
		Columns       int    `json:"columns"`
		Rows          int    `json:"rows"`
		Tmux          string `json:"tmux,omitempty"`
		TmuxSessionID string `json:"tmuxSessionId,omitempty"`
	}{Columns: columns, Rows: rows, Tmux: target.Tmux, TmuxSessionID: target.TmuxSessionID}

	path := "/instances/" + encodeURIComponent(id) + "/terminal/sessions"

	return typed[TerminalSession](ctx, c, call{method: http.MethodPost, path: path, body: body})
}

func (c *Client) Pricing(ctx context.Context, workload Workload) ([]PriceRate, error) {
	path := "/instances/pricing"

	if workload == WorkloadRunJob {
		path = "/run-jobs/pricing"
	}

	return typed[[]PriceRate](ctx, c, call{method: http.MethodGet, path: path})
}

func (c *Client) Templates(ctx context.Context) ([]Template, error) {
	return typed[[]Template](ctx, c, call{method: http.MethodGet, path: "/templates/"})
}

func (c *Client) Template(ctx context.Context, id string) (Template, error) {
	return typed[Template](ctx, c, call{method: http.MethodGet, path: "/templates/" + encodeURIComponent(id)})
}

func (c *Client) CreateTemplate(ctx context.Context, body TemplateBody) (Template, error) {
	return typed[Template](ctx, c, call{method: http.MethodPost, path: "/templates/", body: body})
}

func (c *Client) UpdateTemplate(ctx context.Context, id string, body TemplateBody) (Template, error) {
	request := call{method: http.MethodPatch, path: "/templates/" + encodeURIComponent(id), body: body}
	return typed[Template](ctx, c, request)
}

func (c *Client) DeleteTemplate(ctx context.Context, id string) (int, error) {
	return c.requestStatus(ctx, http.MethodDelete, "/templates/"+encodeURIComponent(id))
}

func (c *Client) Organization(ctx context.Context) (Organization, error) {
	return typed[Organization](ctx, c, call{method: http.MethodGet, path: "/organization", unwrapData: true})
}

func (c *Client) ListVolumes(ctx context.Context) ([]NetworkVolume, error) {
	return typed[[]NetworkVolume](ctx, c, call{method: http.MethodGet, path: "/volumes/"})
}

func (c *Client) CreateVolume(ctx context.Context, body CreateVolumeBody, idempotencyKey string) (NetworkVolume, error) {
	request := call{method: http.MethodPost, path: "/volumes/", body: body, idempotencyKey: idempotencyKey}
	return typed[NetworkVolume](ctx, c, request)
}

func (c *Client) GetVolume(ctx context.Context, id string) (NetworkVolume, error) {
	return typed[NetworkVolume](ctx, c, call{method: http.MethodGet, path: "/volumes/" + encodeURIComponent(id)})
}

func (c *Client) PatchVolume(ctx context.Context, id string, body UpdateVolumeBody) (NetworkVolume, error) {
	request := call{method: http.MethodPatch, path: "/volumes/" + encodeURIComponent(id), body: body}
	return typed[NetworkVolume](ctx, c, request)
}

func (c *Client) CreateFileSession(ctx context.Context, volumeID string) (FileSession, error) {
	path := "/volumes/" + encodeURIComponent(volumeID) + "/files/session"
	return typed[FileSession](ctx, c, call{method: http.MethodPost, path: path})
}

func (c *Client) DeleteVolume(ctx context.Context, id string, detach bool) (int, error) {
	path := "/volumes/" + encodeURIComponent(id)

	if detach {
		path += "?detach=true"
	}

	return c.requestStatus(ctx, http.MethodDelete, path)
}

func (c *Client) ListJobs(ctx context.Context, page, limit int) (RunJobList, error) {
	path := "/run-jobs?page=" + strconv.Itoa(page) + "&limit=" + strconv.Itoa(limit)
	return typed[RunJobList](ctx, c, call{method: http.MethodGet, path: path})
}

func (c *Client) GetJob(ctx context.Context, id string) (RunJob, error) {
	return typed[RunJob](ctx, c, call{method: http.MethodGet, path: "/run-jobs/" + encodeURIComponent(id)})
}

func (c *Client) CreateJob(ctx context.Context, body CreateRunJobBody, idempotencyKey string) (RunJob, error) {
	request := call{method: http.MethodPost, path: "/run-jobs/", body: body, idempotencyKey: idempotencyKey}
	return typed[RunJob](ctx, c, request)
}

func (c *Client) JobLogs(ctx context.Context, id string, tailLines int, section LogSection) (RunJobLogs, error) {
	path := "/run-jobs/" + encodeURIComponent(id) + "/logs" + logsQuery(tailLines, section)
	return typed[RunJobLogs](ctx, c, call{method: http.MethodGet, path: path})
}

func (c *Client) JobLogHistory(ctx context.Context, id, containerRunID string, section LogSection) (RunJobLogHistory, error) {
	query := "containerRunId=" + formEncode(containerRunID) + "&section=" + formEncode(string(section))
	path := "/run-jobs/" + encodeURIComponent(id) + "/logs/history?" + query

	return typed[RunJobLogHistory](ctx, c, call{method: http.MethodGet, path: path})
}

func (c *Client) CancelJob(ctx context.Context, id string) (RunJob, error) {
	path := "/run-jobs/" + encodeURIComponent(id) + "/cancel"
	return typed[RunJob](ctx, c, call{method: http.MethodPost, path: path})
}

func (c *Client) DeleteJob(ctx context.Context, id string) (RunJob, error) {
	return typed[RunJob](ctx, c, call{method: http.MethodDelete, path: "/run-jobs/" + encodeURIComponent(id)})
}

func (c *Client) SSHKeys(ctx context.Context) ([]SSHKey, error) {
	return typed[[]SSHKey](ctx, c, call{method: http.MethodGet, path: "/me/ssh-keys", unwrapData: true})
}

func (c *Client) AddSSHKey(ctx context.Context, body AddSSHKeyBody) (SSHKey, error) {
	request := call{method: http.MethodPost, path: "/me/ssh-keys", body: body, unwrapData: true}
	return typed[SSHKey](ctx, c, request)
}

func (c *Client) DeleteSSHKey(ctx context.Context, id string) (int, error) {
	return c.requestStatus(ctx, http.MethodDelete, "/me/ssh-keys/"+encodeURIComponent(id))
}

func (c *Client) RegistryCredentials(ctx context.Context) ([]RegistryCredential, error) {
	request := call{method: http.MethodGet, path: "/registry-credentials", unwrapData: true}
	return typed[[]RegistryCredential](ctx, c, request)
}

func (c *Client) AddRegistryCredential(ctx context.Context, body RegistryCredentialBody) (RegistryCredential, error) {
	request := call{method: http.MethodPost, path: "/registry-credentials", body: body, unwrapData: true}
	return typed[RegistryCredential](ctx, c, request)
}

func (c *Client) UpdateRegistryCredential(ctx context.Context, id string, body UpdateRegistryCredentialBody) (RegistryCredential, error) {
	path := "/registry-credentials/" + encodeURIComponent(id)
	request := call{method: http.MethodPatch, path: path, body: body, unwrapData: true}

	return typed[RegistryCredential](ctx, c, request)
}

func (c *Client) DeleteRegistryCredential(ctx context.Context, id string) (int, error) {
	return c.requestStatus(ctx, http.MethodDelete, "/registry-credentials/"+encodeURIComponent(id))
}

func (c *Client) BillingSummary(ctx context.Context) (BillingSummary, error) {
	return typed[BillingSummary](ctx, c, call{method: http.MethodGet, path: "/billing"})
}

func (c *Client) BillingLedger(ctx context.Context) (PaginatedLedger, error) {
	return typed[PaginatedLedger](ctx, c, call{method: http.MethodGet, path: "/billing/ledger"})
}

type call struct {
	method         string
	path           string
	body           any
	idempotencyKey string
	// unwrapData reads the schema from the response's { data } envelope.
	unwrapData bool
}

func typed[T any](ctx context.Context, c *Client, request call) (T, error) {
	var zero T

	data, err := c.requestJSON(ctx, request)

	if err != nil {
		return zero, err
	}

	if !request.unwrapData {
		return parse[T](data)
	}

	var envelope map[string]json.RawMessage

	if err := json.Unmarshal(data, &envelope); err != nil {
		return zero, errs.New(unexpectedResponse)
	}

	inner, hasData := envelope["data"]

	if !hasData {
		return zero, errs.New(unexpectedResponse)
	}

	return parse[T](inner)
}

func (c *Client) requestJSON(ctx context.Context, request call) ([]byte, error) {
	response, err := c.send(ctx, request)

	if err != nil {
		return nil, err
	}

	defer response.Body.Close()

	if isFailure(response) {
		return nil, statusError(response)
	}

	data, err := io.ReadAll(response.Body)

	if err != nil {
		return nil, c.connectionError(ctx)
	}

	return bytes.TrimPrefix(data, utf8BOM), nil
}

func (c *Client) requestStatus(ctx context.Context, method, path string) (int, error) {
	response, err := c.send(ctx, call{method: method, path: path})

	if err != nil {
		return 0, err
	}

	defer response.Body.Close()

	if isFailure(response) {
		return 0, statusError(response)
	}

	return response.StatusCode, nil
}

func (c *Client) send(ctx context.Context, request call) (*http.Response, error) {
	var body io.Reader
	hasBody := request.body != nil

	if hasBody {
		encoded, err := json.Marshal(request.body)

		if err != nil {
			return nil, err
		}

		body = bytes.NewReader(encoded)
	}

	httpRequest, err := http.NewRequestWithContext(ctx, request.method, c.apiBase+request.path, body)

	if err != nil {
		return nil, c.connectionError(ctx)
	}

	if request.idempotencyKey != "" {
		httpRequest.Header.Set("Idempotency-Key", request.idempotencyKey)
	}

	httpRequest.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpRequest.Header.Set("User-Agent", "featherless-cloud/"+version.Version)

	if hasBody {
		httpRequest.Header.Set("Content-Type", "application/json")
	}

	response, err := c.http.Do(httpRequest)

	if err != nil {
		return nil, c.connectionError(ctx)
	}

	return response, nil
}

func (c *Client) connectionError(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}

	return errs.Retryable("Could not connect to " + c.apiBase)
}

func isFailure(response *http.Response) bool {
	return response.StatusCode < 200 || response.StatusCode > 299
}

// statusError reads a failed response's body like response.text(): empty
// when it cannot be read.
func statusError(response *http.Response) error {
	data, err := io.ReadAll(response.Body)
	text := ""

	if err == nil {
		text = string(bytes.TrimPrefix(data, utf8BOM))
	}

	return ErrorFromStatus(response.StatusCode, text)
}

func logsQuery(tailLines int, section LogSection) string {
	query := "?section=" + string(section)

	if tailLines != 0 {
		query += "&tailLines=" + strconv.Itoa(tailLines)
	}

	return query
}

// encodeURIComponent escapes all but A-Z a-z 0-9 - _ . ! ~ * ' ( ), like JavaScript's.
func encodeURIComponent(value string) string {
	return percentEncode(value, "-_.!~*'()", false)
}

// formEncode escapes like URLSearchParams: all but A-Z a-z 0-9 * - . _, space as +.
func formEncode(value string) string {
	return percentEncode(value, "*-._", true)
}

func percentEncode(value, unreserved string, isSpacePlus bool) string {
	var builder strings.Builder

	for _, b := range []byte(value) {
		isAlphanumeric := ('a' <= b && b <= 'z') || ('A' <= b && b <= 'Z') || ('0' <= b && b <= '9')

		switch {
		case isAlphanumeric || strings.IndexByte(unreserved, b) >= 0:
			builder.WriteByte(b)
		case b == ' ' && isSpacePlus:
			builder.WriteByte('+')
		default:
			fmt.Fprintf(&builder, "%%%02X", b)
		}
	}

	return builder.String()
}
