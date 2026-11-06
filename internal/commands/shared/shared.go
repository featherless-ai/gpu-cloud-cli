// Package shared holds what several command groups use: credentials, name
// resolution, completers and prompts.
package shared

import (
	"context"
	"regexp"

	"github.com/spf13/cobra"

	"featherless.ai/featherless-cloud/internal/api"
	"featherless.ai/featherless-cloud/internal/completion"
	"featherless.ai/featherless-cloud/internal/config"
	"featherless.ai/featherless-cloud/internal/errs"
	"featherless.ai/featherless-cloud/internal/output"
)

// Seams tests replace.
var (
	ResolveAuth = config.ResolveAuth
	LoadConfig  = config.Load
	NewCloud    = func(apiBase, apiKey string) api.Cloud { return api.NewClient(apiBase, apiKey) }
	// NewContext is the output context a command writes to.
	NewContext = output.FromCommand
)

var UUIDPattern = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// Client returns an API client for the --profile value, or the current profile.
func Client(profile string) (api.Cloud, error) {
	auth, err := ResolveAuth(profile)
	if err != nil {
		return nil, err
	}

	return NewCloud(auth.APIBase, auth.APIKey), nil
}

// StartsWith keeps the values that start with word, ignoring case.
func StartsWith(values []string, word string) []string {
	return nil
}

// ParseRepeatable splits repeated NAME=VALUE flag values on the first =.
func ParseRepeatable(values []string, label string) ([]api.EnvVar, error) {
	return nil, errs.New("not implemented")
}

// ParsePorts reads repeated --port values as public ports; none alone means no ports.
func ParsePorts(values []string) ([]int, error) {
	return nil, errs.New("not implemented")
}

// ToEnvironment checks environment variable names are shell-compatible and unique.
func ToEnvironment(pairs []api.EnvVar) ([]api.EnvVar, error) {
	return nil, errs.New("not implemented")
}

func ValidateMounts(mounts []api.NetworkVolumeMount) error {
	return errs.New("not implemented")
}

// VolumeMounts completes `--volume <name:path>` as `<name>:`; the mount path
// after the colon is free text.
func VolumeMounts(word string, ctx *completion.Context) ([]string, error) {
	return nil, errs.New("not implemented")
}

// ComputeOptions are the compute flags; nil means the flag was not given.
type ComputeOptions struct {
	ComputeType api.ComputeType
	GPUType     *string
	GPUCount    *string
	CPUCores    *string
	// Interactive runs prompt instead of failing when no flags were given.
	Interactive bool
}

// ResolveCompute turns --gpu-type, --gpu-count and --cpu-cores into a compute SKU.
func ResolveCompute(prices []api.PriceRate, opts ComputeOptions, ctx *output.Context) (string, error) {
	return "", errs.New("not implemented")
}

// GPUTypes completes the GPU models on offer, lowercased like the --gpu-type examples.
func GPUTypes(workload api.Workload) completion.Completer {
	return func(word string, ctx *completion.Context) ([]string, error) {
		return nil, errs.New("not implemented")
	}
}

func GPUCounts(workload api.Workload) completion.Completer {
	return func(word string, ctx *completion.Context) ([]string, error) {
		return nil, errs.New("not implemented")
	}
}

func CPUCores(workload api.Workload) completion.Completer {
	return func(word string, ctx *completion.Context) ([]string, error) {
		return nil, errs.New("not implemented")
	}
}

type ImageOptions struct {
	Image    string
	Registry string
}

// ImageFields are the body fields --image and --registry set; empty when not set.
type ImageFields struct {
	ContainerImage       string
	RegistryCredentialID string
}

// ResolveImage reads --image and --registry; with a template they replace its
// image or registry key.
func ResolveImage(credentials []api.RegistryCredential, opts ImageOptions, hasTemplate bool) (ImageFields, error) {
	return ImageFields{}, errs.New("not implemented")
}

// ImageChoice is the template, or else the container image, picked at the prompt.
type ImageChoice struct {
	Template       *api.Template
	ContainerImage string
}

// SelectImage asks for a template when neither a template nor --image was given.
func SelectImage(templates []api.Template, ctx *output.Context) (ImageChoice, error) {
	return ImageChoice{}, errs.New("not implemented")
}

// TemplateReference is what to type for a template: its name, qualified only
// when one closer to you has the same name. A bare name finds your template,
// then your organization's, then an official one.
func TemplateReference(template api.Template, templates []api.Template) string {
	return ""
}

// ResolveTemplateRef finds --template: an id, a name, org/<name>,
// featherless/<name>, or an official template's display name.
func ResolveTemplateRef(templates []api.Template, ref string) (api.Template, error) {
	return api.Template{}, errs.New("not implemented")
}

// SplitTemplateVersion reads featherless/rocm-pytorch:2.11.0 as an official
// template's version; template names never hold a colon.
func SplitTemplateVersion(ref string) (name, version string) {
	return "", ""
}

// CheckTemplateVersion accepts only a version the official template lists.
func CheckTemplateVersion(template api.Template, version string) (string, error) {
	return "", errs.New("not implemented")
}

type TemplateOptions struct {
	Template        string
	TemplateID      string
	TemplateVersion string
}

type TemplateChoice struct {
	Template api.Template
	// Version is an official version other than the current one.
	Version string
}

// FindTemplate returns the template --template or --template-id names, or nil.
func FindTemplate(ctx context.Context, client api.Cloud, opts TemplateOptions) (*TemplateChoice, error) {
	return nil, errs.New("not implemented")
}

// Templates completes what to type for each enabled template.
func Templates(word string, ctx *completion.Context) ([]string, error) {
	return nil, errs.New("not implemented")
}

func TemplateIDs(word string, ctx *completion.Context) ([]string, error) {
	return nil, errs.New("not implemented")
}

// VolumeFlag is one --volume NAME:PATH.
type VolumeFlag struct {
	Volume    string
	MountPath string
}

func ParseVolumeFlags(values []string) ([]VolumeFlag, error) {
	return nil, errs.New("not implemented")
}

func ParseDisk(value string) (int, error) {
	return 0, errs.New("not implemented")
}

// ComputeFlagsGiven reports whether any compute flag was typed, so a
// template's compute gives way.
func ComputeFlagsGiven(cmd *cobra.Command) bool {
	return false
}

func ResolveCredentialRef(credentials []api.RegistryCredential, ref string) (string, error) {
	return "", errs.New("not implemented")
}

func Registries(word string, ctx *completion.Context) ([]string, error) {
	return nil, errs.New("not implemented")
}

func ResolveSSHKeyRef(keys []api.SSHKey, ref string) (api.SSHKey, error) {
	return api.SSHKey{}, errs.New("not implemented")
}

func SSHKeys(word string, ctx *completion.Context) ([]string, error) {
	return nil, errs.New("not implemented")
}

// FindVolume finds a volume by uuid, exact name, or unique substring, in any state.
func FindVolume(volumes []api.NetworkVolume, ref string) (api.NetworkVolume, error) {
	return api.NetworkVolume{}, errs.New("not implemented")
}

// ResolveVolume returns the id of an attachable (available or in use) volume.
func ResolveVolume(volumes []api.NetworkVolume, ref string) (string, error) {
	return "", errs.New("not implemented")
}

func Volumes(word string, ctx *completion.Context) ([]string, error) {
	return nil, errs.New("not implemented")
}

// FindInstance finds an instance by uuid, exact name, or unique substring, in any state.
func FindInstance(instances []api.Instance, ref string) (api.Instance, error) {
	return api.Instance{}, errs.New("not implemented")
}

// ResolveInstanceID turns an instance reference (uuid, short id, name, unique
// substring) into the id the API expects.
func ResolveInstanceID(ctx context.Context, client api.Cloud, ref string) (string, error) {
	return "", errs.New("not implemented")
}

// Instances completes instance names and short ids.
func Instances(word string, ctx *completion.Context) ([]string, error) {
	return nil, errs.New("not implemented")
}

// Jobs completes Run Job short ids; job commands take a uuid or short id.
func Jobs(word string, ctx *completion.Context) ([]string, error) {
	return nil, errs.New("not implemented")
}

// RandomInstanceName is a friendly name, as the console launcher picks.
func RandomInstanceName() string {
	return ""
}

// AssertInstanceName applies the naming rule the API enforces.
func AssertInstanceName(name string) error {
	return errs.New("not implemented")
}

// RequireDeletion confirms a destructive operation; non-interactive runs need --yes.
func RequireDeletion(ctx *output.Context, label string, yes bool) error {
	return errs.New("not implemented")
}

func Profiles(word string, ctx *completion.Context) ([]string, error) {
	return nil, errs.New("not implemented")
}
