package api

type TemplateVisibility string

const (
	VisibilityPrivate      TemplateVisibility = "private"
	VisibilityOrganization TemplateVisibility = "organization"
	VisibilityPublic       TemplateVisibility = "public"
)

// TemplateVisibilities are what an owner may set; official templates are public.
var TemplateVisibilities = []string{string(VisibilityPrivate), string(VisibilityOrganization)}

type AutoTmux string

const (
	AutoTmuxOff     AutoTmux = "off"
	AutoTmuxSSHOnly AutoTmux = "ssh-only"
)

type GPUVendor string

const (
	VendorAMD    GPUVendor = "amd"
	VendorNVIDIA GPUVendor = "nvidia"
)

// Template is a GET /templates item: an official template, your own, or one
// shared in your organization.
type Template struct {
	ID                   string               `json:"id"`
	Name                 string               `json:"name"`
	Description          string               `json:"description"`
	ImageRepository      *string              `json:"imageRepository"`
	Enabled              bool                 `json:"enabled"`
	ComputeTypes         []ComputeType        `json:"computeTypes"`
	Version              CatalogVersion       `json:"version"`
	ShortID              string               `json:"shortId"`
	Slug                 string               `json:"slug"`
	Official             bool                 `json:"official"`
	Visibility           TemplateVisibility   `json:"visibility"`
	Owner                *TemplateOwner       `json:"owner"`
	CanEdit              bool                 `json:"canEdit"`
	Image                string               `json:"image"`
	ImageSource          ImageSource          `json:"imageSource"`
	RegistryCredentialID *string              `json:"registryCredentialId"`
	ComputeSKU           *string              `json:"computeSku"`
	EphemeralDiskGiB     *int                 `json:"ephemeralDiskGiB"`
	NetworkVolumeMounts  []NetworkVolumeMount `json:"networkVolumeMounts"`
	Command              []string             `json:"command"`
	Environment          []EnvVar             `json:"environment"`
	SecretNames          []string             `json:"secretNames"`
	PublicPorts          []int                `json:"publicPorts"`
	AutoTmux             *AutoTmux            `json:"autoTmux"`
	Tags                 []string             `json:"tags" default:"[]"`
	// GPUVendors are the GPUs its image runs on.
	GPUVendors []GPUVendor `json:"gpuVendors" default:"[\"amd\",\"nvidia\"]"`
	// Versions are an official template's versions, current first.
	Versions []TemplateVersionOption `json:"versions" default:"[]"`
}

// CatalogVersion is the template's single published version.
type CatalogVersion struct {
	ID             string  `json:"id"`
	Version        string  `json:"version"`
	Tag            *string `json:"tag"`
	State          string  `json:"state"`
	Image          string  `json:"image"`
	StartupCommand string  `json:"startupCommand"`
}

type TemplateOwner struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type TemplateVersionOption struct {
	Version string `json:"version"`
	Image   string `json:"image"`
}

type NetworkVolumeMount struct {
	NetworkVolumeID string `json:"networkVolumeId"`
	MountPath       string `json:"mountPath"`
}
