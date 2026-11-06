package api

type VolumeState string

const (
	VolumeProvisioning VolumeState = "provisioning"
	VolumeAvailable    VolumeState = "available"
	VolumeResizing     VolumeState = "resizing"
	VolumeError        VolumeState = "error"
	VolumeDeleting     VolumeState = "deleting"
	VolumeDeleted      VolumeState = "deleted"
	VolumeInUse        VolumeState = "in_use"
)

type NetworkVolume struct {
	ID          string             `json:"id"`
	Name        string             `json:"name"`
	CapacityGiB int                `json:"capacityGiB"`
	State       VolumeState        `json:"state"`
	Attachments []VolumeAttachment `json:"attachments"`
	Pricing     VolumePricing      `json:"pricing"`
	LastError   *string            `json:"lastError"`
	CreatedAt   string             `json:"createdAt"`
	UpdatedAt   string             `json:"updatedAt"`
}

// VolumeAttachment is an instance the volume is mounted on.
type VolumeAttachment struct {
	ID          string      `json:"id"`
	Name        string      `json:"name"`
	ShortID     string      `json:"shortId"`
	GPUCount    int         `json:"gpuCount"`
	ComputeType ComputeType `json:"computeType"`
	CPUCores    int         `json:"cpuCores"`
	MountPath   string      `json:"mountPath"`
}

type VolumePricing struct {
	UnitPriceMicrosPerGiBMonth string `json:"unitPriceMicrosPerGiBMonth"`
	EstimatedMicrosPerMonth    string `json:"estimatedMicrosPerMonth"`
	BillingActive              bool   `json:"billingActive"`
}
