package api

import (
	"bytes"
	"reflect"
	"slices"
	"strings"
)

type ComputeType string

const (
	ComputeGPU ComputeType = "gpu"
	ComputeCPU ComputeType = "cpu"
)

var _ = registerEnum(ComputeGPU, ComputeCPU)

type InstanceState string

const (
	InstanceReserving  InstanceState = "reserving"
	InstanceQueued     InstanceState = "queued"
	InstanceStarting   InstanceState = "starting"
	InstanceRunning    InstanceState = "running"
	InstanceStopping   InstanceState = "stopping"
	InstanceStopped    InstanceState = "stopped"
	InstanceRestarting InstanceState = "restarting"
	InstanceCompleted  InstanceState = "completed"
	InstanceFailed     InstanceState = "failed"
	InstanceDeleting   InstanceState = "deleting"
	InstanceDeleted    InstanceState = "deleted"
)

var _ = registerEnum(
	InstanceReserving, InstanceQueued, InstanceStarting, InstanceRunning, InstanceStopping, InstanceStopped,
	InstanceRestarting, InstanceCompleted, InstanceFailed, InstanceDeleting, InstanceDeleted,
)

type StartupStage string

const (
	StageWaitingForGPU         StartupStage = "waiting_for_gpu"
	StageWaitingForCPU         StartupStage = "waiting_for_cpu"
	StagePreparingContainer    StartupStage = "preparing_container"
	StageDownloadingImage      StartupStage = "downloading_image"
	StageStartingContainer     StartupStage = "starting_container"
	StageRetryingImageDownload StartupStage = "retrying_image_download"
)

var _ = registerEnum(
	StageWaitingForGPU, StageWaitingForCPU, StagePreparingContainer, StageDownloadingImage,
	StageStartingContainer, StageRetryingImageDownload,
)

type ImageSource string

const (
	ImageSourceTemplate ImageSource = "template"
	ImageSourceCustom   ImageSource = "custom"
)

var _ = registerEnum(ImageSourceTemplate, ImageSourceCustom)

type Resources struct {
	GPUModel     *string `json:"gpuModel"`
	GPUMemoryGiB *int    `json:"gpuMemoryGiB"`
	CPUCores     int     `json:"cpuCores"`
	MemoryGiB    int     `json:"memoryGiB"`
}

type EnvVar struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Instance mirrors the customer-facing instance fields the CLI renders.
type Instance struct {
	ID                  string                           `json:"id"`
	ShortID             string                           `json:"shortId"`
	Name                string                           `json:"name"`
	ComputeLabel        string                           `json:"computeLabel"`
	ComputeType         ComputeType                      `json:"computeType"`
	ComputeSKU          *string                          `json:"computeSku,omitzero"`
	GPUCount            int                              `json:"gpuCount"`
	State               InstanceState                    `json:"state"`
	StartupStage        *StartupStage                    `json:"startupStage"`
	CostMicrosPerHour   string                           `json:"costMicrosPerHour"`
	EphemeralDiskGiB    int                              `json:"ephemeralDiskGiB"`
	VolumeDiskGiB       Nullish[int]                     `json:"volumeDiskGiB,omitzero"`
	NetworkVolumeMounts []InstanceVolumeMount            `json:"networkVolumeMounts,omitzero"`
	PublicPorts         []PublicPort                     `json:"publicPorts,omitzero"`
	Resources           *Resources                       `json:"resources,omitzero"`
	Pricing             *InstancePricing                 `json:"pricing,omitzero"`
	RuntimeStartedAt    Nullish[string]                  `json:"runtimeStartedAt,omitzero"`
	TemplateVersion     Nullish[InstanceTemplateVersion] `json:"templateVersion,omitzero"`
	Template            Nullish[TemplateRef]             `json:"template,omitzero"`
	Image               *InstanceImage                   `json:"image,omitzero"`
	Command             []string                         `json:"command,omitzero"`
	Environment         []EnvVar                         `json:"environment,omitzero"`
	SecretNames         []string                         `json:"secretNames,omitzero"`
	LastError           Nullish[string]                  `json:"lastError,omitzero"`
	SSH                 *InstanceSSH                     `json:"ssh,omitzero"`
	CreatedAt           string                           `json:"createdAt"`
	UpdatedAt           *string                          `json:"updatedAt,omitzero"`
	DeletedAt           Nullish[string]                  `json:"deletedAt,omitzero"`
}

type InstanceVolumeMount struct {
	MountPath string        `json:"mountPath"`
	Volume    MountedVolume `json:"volume"`
}

// MountedVolume passes through every volume field the API sends.
type MountedVolume struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	CapacityGiB int    `json:"capacityGiB"`
	// Extra holds the fields beyond these, encoded after them in API order.
	Extra []RawField `json:"-"`
}

func (v *MountedVolume) decodeNode(node any) bool {
	fields, ok := node.(object)

	if !ok {
		return false
	}

	v.Extra = nil

	if !decodeFields(reflect.ValueOf(v).Elem(), fields) {
		return false
	}

	volumeType := reflect.TypeFor[MountedVolume]()
	var known []string

	for i := range volumeType.NumField() {
		name, _, _ := strings.Cut(volumeType.Field(i).Tag.Get("json"), ",")
		known = append(known, name)
	}

	for _, field := range fields {
		if slices.Contains(known, field.name) {
			continue
		}

		var value bytes.Buffer
		writeNode(&value, field.value)
		v.Extra = append(v.Extra, RawField{Name: field.name, Value: value.Bytes()})
	}

	return true
}

func (v MountedVolume) MarshalJSON() ([]byte, error) {
	type schemaFields MountedVolume

	encoded, err := marshalUnescaped(schemaFields(v))

	if err != nil || len(v.Extra) == 0 {
		return encoded, err
	}

	var buffer bytes.Buffer
	buffer.Write(bytes.TrimSuffix(encoded, []byte("}")))

	for _, extra := range v.Extra {
		name, err := marshalUnescaped(extra.Name)

		if err != nil {
			return nil, err
		}

		buffer.WriteByte(',')
		buffer.Write(name)
		buffer.WriteByte(':')

		if len(extra.Value) == 0 {
			buffer.WriteString("null")
		} else {
			buffer.Write(extra.Value)
		}
	}

	buffer.WriteByte('}')

	return buffer.Bytes(), nil
}

type PublicPort struct {
	Port int     `json:"port"`
	URL  *string `json:"url"`
}

type InstancePricing struct {
	ComputeMicrosPerHour    string `json:"computeMicrosPerHour"`
	DiskMicrosPerHour       string `json:"diskMicrosPerHour"`
	VolumeDiskMicrosPerHour string `json:"volumeDiskMicrosPerHour"`
	BillingActive           bool   `json:"billingActive"`
	VolumeDiskBillingActive bool   `json:"volumeDiskBillingActive"`
}

// InstanceTemplateVersion is the template version an instance was launched from.
type InstanceTemplateVersion struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Name    string `json:"name"`
	Image   string `json:"image"`
}

type TemplateRef struct {
	ID      string `json:"id"`
	ShortID string `json:"shortId"`
	Name    string `json:"name"`
}

type InstanceImage struct {
	Source             ImageSource      `json:"source"`
	Reference          string           `json:"reference"`
	RequestedReference string           `json:"requestedReference"`
	RegistryCredential *ImageCredential `json:"registryCredential"`
}

type ImageCredential struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Registry string `json:"registry"`
}

type InstanceSSH struct {
	Host    string  `json:"host"`
	HasKeys bool    `json:"hasKeys"`
	Command *string `json:"command"`
	Status  string  `json:"status"`
}

type Pagination struct {
	Page       int `json:"page"`
	PageSize   int `json:"pageSize"`
	TotalItems int `json:"totalItems"`
	TotalPages int `json:"totalPages"`
}

type InstanceList struct {
	Items      []Instance `json:"items"`
	Pagination Pagination `json:"pagination"`
}

type EventType string

const (
	EventNormal  EventType = "Normal"
	EventWarning EventType = "Warning"
)

var _ = registerEnum(EventNormal, EventWarning)

// InstanceEvent is a GET /instances/:id/history item.
type InstanceEvent struct {
	ID              string      `json:"id"`
	Type            EventType   `json:"type"`
	Reason          string      `json:"reason"`
	Message         string      `json:"message"`
	FirstObservedAt string      `json:"firstObservedAt"`
	LastObservedAt  string      `json:"lastObservedAt"`
	Count           int         `json:"count"`
	Actor           *EventActor `json:"actor"`
}

type EventActor struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

type InstanceLogs struct {
	Logs string `json:"logs"`
}

type Role string

const (
	RoleOwner  Role = "owner"
	RoleAdmin  Role = "admin"
	RoleMember Role = "member"
)

var _ = registerEnum(RoleOwner, RoleAdmin, RoleMember)

// Me is GET /me (session or users:read key).
type Me struct {
	Data MeData `json:"data"`
}

type MeData struct {
	User         User            `json:"user"`
	Organization OrganizationRef `json:"organization"`
	Membership   Membership      `json:"membership"`
}

type User struct {
	ID            string  `json:"id"`
	Name          string  `json:"name"`
	Email         string  `json:"email"`
	EmailVerified bool    `json:"emailVerified"`
	Image         *string `json:"image"`
}

type OrganizationRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

type Membership struct {
	ID   string `json:"id"`
	Role Role   `json:"role"`
}

// Organization is GET /organization, unwrapped from { data }.
type Organization struct {
	Organization OrganizationRef `json:"organization"`
	Membership   Membership      `json:"membership"`
	GPUCount     int             `json:"gpuCount"`
	GPULimit     int             `json:"gpuLimit"`
	CPUCount     int             `json:"cpuCount"`
	CPULimit     int             `json:"cpuLimit"`
}

type SSHKey struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Fingerprint string `json:"fingerprint"`
	CreatedAt   string `json:"createdAt"`
}

type RegistryCredential struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Registry  string `json:"registry"`
	Username  string `json:"username"`
	UpdatedAt string `json:"updatedAt"`
}

// BillingSummary is GET /billing (requires a users:read key).
type BillingSummary struct {
	BalanceMicros      string     `json:"balanceMicros"`
	Currency           string     `json:"currency"`
	TopUpPresetsMicros []string   `json:"topUpPresetsMicros"`
	StripeEnabled      bool       `json:"stripeEnabled"`
	MeteringEnabled    bool       `json:"meteringEnabled"`
	EnforcementEnabled bool       `json:"enforcementEnabled"`
	AutoReload         AutoReload `json:"autoReload"`
}

type AutoReload struct {
	Enabled            bool    `json:"enabled"`
	ThresholdMicros    string  `json:"thresholdMicros"`
	AmountMicros       string  `json:"amountMicros"`
	PaymentMethodReady bool    `json:"paymentMethodReady"`
	ConsentedAt        *string `json:"consentedAt"`
}

type LedgerEntryType string

const (
	LedgerTopUp      LedgerEntryType = "top_up"
	LedgerUsage      LedgerEntryType = "usage"
	LedgerAdjustment LedgerEntryType = "adjustment"
	LedgerRefund     LedgerEntryType = "refund"
)

var _ = registerEnum(LedgerTopUp, LedgerUsage, LedgerAdjustment, LedgerRefund)

type ResourceKind string

const (
	ResourceInstance       ResourceKind = "instance"
	ResourceJob            ResourceKind = "job"
	ResourceNetworkStorage ResourceKind = "network_storage"
)

var _ = registerEnum(ResourceInstance, ResourceJob, ResourceNetworkStorage)

// LedgerEntry is one resource, one sku, one organization-local day.
type LedgerEntry struct {
	ID             string          `json:"id"`
	Type           LedgerEntryType `json:"type"`
	AmountMicros   string          `json:"amountMicros"`
	Description    string          `json:"description"`
	ResourceKind   *ResourceKind   `json:"resourceKind"`
	ResourceName   *string         `json:"resourceName"`
	SKUDisplayName *string         `json:"skuDisplayName"`
	Unit           *string         `json:"unit"`
	Quantity       *string         `json:"quantity"`
	ChargedAt      string          `json:"chargedAt"`
	ChargedUntil   string          `json:"chargedUntil"`
}

type PaginatedLedger struct {
	Items      []LedgerEntry    `json:"items"`
	Pagination LedgerPagination `json:"pagination"`
}

// LedgerPagination numbers are any JSON number, not only integers.
type LedgerPagination struct {
	Page       float64 `json:"page"`
	PageSize   float64 `json:"pageSize"`
	TotalItems float64 `json:"totalItems"`
	TotalPages float64 `json:"totalPages"`
}
