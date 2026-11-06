// Package shared holds what several command groups use: credentials, name
// resolution, completers and prompts.
package shared

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

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

var (
	UUIDPattern       = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	shortIDPattern    = regexp.MustCompile(`^[0123456789bcdfghjkmnpqrstvwxyz]{10}$`)
	digitsPattern     = regexp.MustCompile(`^[0-9]+$`)
	envNamePattern    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	mountPathPattern  = regexp.MustCompile(`^/(?:[^/]+/)*[^/]+$`)
	instanceName      = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
	inServiceGPUModel = regexp.MustCompile(`(?i)mi[-_ ]?325`)
)

// Client returns an API client for the --profile value, or the current profile.
func Client(profile string) (api.Cloud, error) {
	auth, err := ResolveAuth(profile)
	if err != nil {
		return nil, err
	}

	return NewCloud(auth.APIBase, auth.APIKey), nil
}

// Cancelled ends a command whose prompt was cancelled or declined.
func Cancelled(ctx *output.Context) error {
	ctx.Log("Cancelled.")
	return &errs.Exit{Code: 0}
}

// PromptFailure ends the command quietly when the prompt was cancelled.
func PromptFailure(ctx *output.Context, err error) error {
	if errors.Is(err, output.ErrCancelled) {
		return Cancelled(ctx)
	}

	return err
}

// FirstError is the first failure among requests made together.
func FirstError(failures ...error) error {
	for _, err := range failures {
		if err != nil {
			return err
		}
	}

	return nil
}

// NewIdempotencyKey is a random UUID v4, as crypto.randomUUID() makes.
func NewIdempotencyKey() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80

	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// StartsWith keeps the values that start with word, ignoring case.
func StartsWith(values []string, word string) []string {
	needle := strings.ToLower(word)
	var matches []string

	for _, value := range values {
		if strings.HasPrefix(strings.ToLower(value), needle) {
			matches = append(matches, value)
		}
	}

	return matches
}

// ParseRepeatable splits repeated NAME=VALUE flag values on the first =.
func ParseRepeatable(values []string, label string) ([]api.EnvVar, error) {
	pairs := []api.EnvVar{}

	for _, entry := range values {
		index := strings.Index(entry, "=")

		if index < 1 {
			return nil, errs.Newf("--%s expects NAME=VALUE, got \"%s\"", label, entry)
		}

		pairs = append(pairs, api.EnvVar{Name: entry[:index], Value: entry[index+1:]})
	}

	return pairs, nil
}

// ParsePorts reads repeated --port values as public ports; none alone means no ports.
func ParsePorts(values []string) ([]int, error) {
	ports := []int{}

	if len(values) == 1 && values[0] == "none" {
		return ports, nil
	}

	for _, value := range values {
		port, err := strconv.Atoi(value)
		isPort := digitsPattern.MatchString(value) && err == nil && port >= 1 && port <= 65_535

		if !isPort {
			return nil, errs.Newf("--port expects a port from 1 to 65535, got \"%s\"", value)
		}

		ports = append(ports, port)
	}

	sorted := slices.Clone(ports)
	slices.Sort(sorted)

	if len(slices.Compact(sorted)) != len(ports) {
		return nil, errs.New("List each port once.")
	}

	return ports, nil
}

// ToEnvironment checks environment variable names are shell-compatible and unique.
func ToEnvironment(pairs []api.EnvVar) ([]api.EnvVar, error) {
	seen := map[string]bool{}

	for _, pair := range pairs {
		if !envNamePattern.MatchString(pair.Name) || seen[pair.Name] {
			return nil, errs.New("Variable names must be valid and unique.")
		}

		seen[pair.Name] = true
	}

	return pairs, nil
}

func ValidateMounts(mounts []api.NetworkVolumeMount) error {
	volumeIDs := map[string]bool{}

	for _, mount := range mounts {
		if mount.NetworkVolumeID == "" {
			return errs.New("Choose a network volume for each mount.")
		}

		volumeIDs[mount.NetworkVolumeID] = true
	}

	if len(volumeIDs) != len(mounts) {
		return errs.New("Each network volume can be mounted only once.")
	}

	for _, mount := range mounts {
		if !mountPathPattern.MatchString(mount.MountPath) {
			return errs.New("Use an absolute mount path such as /workspace.")
		}
	}

	for i, mount := range mounts {
		for j, other := range mounts {
			isOverlap := other.MountPath == mount.MountPath ||
				strings.HasPrefix(other.MountPath, mount.MountPath+"/") ||
				strings.HasPrefix(mount.MountPath, other.MountPath+"/")

			if i != j && isOverlap {
				return errs.New("Mount paths must not overlap.")
			}
		}
	}

	return nil
}

// VolumeMounts completes `--volume <name:path>` as `<name>:`; the mount path
// after the colon is free text.
func VolumeMounts(word string, ctx *completion.Context) ([]string, error) {
	if strings.Contains(word, ":") {
		return []string{}, nil
	}

	names, err := Volumes(word, ctx)
	if err != nil {
		return nil, err
	}

	mounts := make([]string, len(names))

	for i, name := range names {
		mounts[i] = name + ":"
	}

	return mounts, nil
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
	isExplicit := opts.ComputeType != api.ComputeGPU || opts.GPUType != nil || opts.GPUCount != nil || opts.CPUCores != nil

	if opts.ComputeType == api.ComputeCPU {
		return resolveCPUSKU(prices, opts.CPUCores)
	}

	if !isExplicit {
		if opts.Interactive {
			return selectCompute(prices, ctx)
		}

		return resolveGPUSKU(prices, "", new("1"))
	}

	gpuType := ""

	if opts.GPUType != nil {
		gpuType = *opts.GPUType
	}

	return resolveGPUSKU(prices, gpuType, opts.GPUCount)
}

func isGPUOffer(rate api.PriceRate, count float64) bool {
	isGPU := api.ComputeTypeOf(rate) == api.ComputeGPU && rate.Unit == api.UnitGPUHour

	return isGPU && rate.GPUCount != nil && float64(*rate.GPUCount) == count
}

func gpuModel(rate api.PriceRate) (string, bool) {
	if rate.Resources == nil || rate.Resources.GPUModel == nil {
		return "", false
	}

	return *rate.Resources.GPUModel, true
}

func resolveGPUSKU(prices []api.PriceRate, gpuType string, gpuCountFlag *string) (string, error) {
	gpuCount := 1.0

	if gpuCountFlag != nil {
		gpuCount = ParseNumber(*gpuCountFlag)
	}

	if !slices.Contains([]float64{1, 2, 4, 8}, gpuCount) {
		return "", errs.New("--gpu-count must be 1, 2, 4, or 8")
	}

	var candidates []api.PriceRate

	for _, rate := range prices {
		if isGPUOffer(rate, gpuCount) {
			candidates = append(candidates, rate)
		}
	}

	if gpuType != "" {
		needle := strings.ToLower(gpuType)

		candidates = slices.DeleteFunc(candidates, func(rate api.PriceRate) bool {
			model, hasModel := gpuModel(rate)

			if !hasModel {
				model = rate.DisplayName
			}

			return !strings.Contains(strings.ToLower(model), needle)
		})
	}

	if len(candidates) > 1 && gpuType == "" {
		// Priced models the fleet may not stock yet make bare --gpu-count
		// ambiguous; default to the in-service model, as the console does.
		stocked := slices.DeleteFunc(slices.Clone(candidates), func(rate api.PriceRate) bool {
			model, _ := gpuModel(rate)
			return !inServiceGPUModel.MatchString(model)
		})

		if len(stocked) == 1 {
			candidates = stocked
		}
	}

	if len(candidates) > 1 {
		names := make([]string, len(candidates))

		for i, rate := range candidates {
			names[i] = rate.DisplayName
		}

		return "", errs.Newf("Multiple GPU offers match (%s). Narrow with --gpu-type.", strings.Join(names, ", "))
	}

	if len(candidates) == 0 {
		return "", errs.New("No GPU offer available. Run featherless-cloud pricing list.")
	}

	return candidates[0].SKU, nil
}

func cpuOffers(prices []api.PriceRate) []api.PriceRate {
	var offers []api.PriceRate

	for _, rate := range prices {
		isCPU := api.ComputeTypeOf(rate) == api.ComputeCPU && rate.Unit == api.UnitVCPUHour && rate.Resources != nil

		if isCPU {
			offers = append(offers, rate)
		}
	}

	return offers
}

func resolveCPUSKU(prices []api.PriceRate, cpuCoresFlag *string) (string, error) {
	candidates := cpuOffers(prices)

	if len(candidates) == 0 {
		return "", errs.New("No CPU offer available. Run featherless-cloud pricing list.")
	}

	if cpuCoresFlag == nil {
		smallest := slices.MinFunc(candidates, func(a, b api.PriceRate) int {
			return a.Resources.CPUCores - b.Resources.CPUCores
		})

		return smallest.SKU, nil
	}

	cores := ParseNumber(*cpuCoresFlag)

	for _, rate := range candidates {
		if float64(rate.Resources.CPUCores) == cores {
			return rate.SKU, nil
		}
	}

	sizes := make([]string, len(candidates))

	for i, rate := range candidates {
		sizes[i] = strconv.Itoa(rate.Resources.CPUCores)
	}

	return "", errs.Newf("No CPU offer with %s cores. Available core counts: %s.", FormatNumber(cores), strings.Join(sizes, ", "))
}

// computeRates are the workload's offers of one kind of compute.
func computeRates(ctx *completion.Context, workload api.Workload, kind api.ComputeType) ([]api.PriceRate, error) {
	unit := api.UnitGPUHour

	if kind == api.ComputeCPU {
		unit = api.UnitVCPUHour
	}

	cloud, err := ctx.Cloud()
	if err != nil {
		return nil, err
	}

	prices, err := cloud.Pricing(context.Background(), workload)
	if err != nil {
		return nil, err
	}

	var rates []api.PriceRate

	for _, rate := range prices {
		if api.ComputeTypeOf(rate) == kind && rate.Unit == unit {
			rates = append(rates, rate)
		}
	}

	return rates, nil
}

// numbers are the distinct values, smallest first.
func numbers(values []int) []string {
	slices.Sort(values)
	values = slices.Compact(values)
	texts := make([]string, len(values))

	for i, value := range values {
		texts[i] = strconv.Itoa(value)
	}

	return texts
}

// GPUTypes completes the GPU models on offer, lowercased like the --gpu-type examples.
func GPUTypes(workload api.Workload) completion.Completer {
	return func(word string, ctx *completion.Context) ([]string, error) {
		rates, err := computeRates(ctx, workload, api.ComputeGPU)
		if err != nil {
			return nil, err
		}

		var models []string

		for _, rate := range rates {
			model, hasModel := gpuModel(rate)
			lowered := strings.ToLower(model)

			if hasModel && !slices.Contains(models, lowered) {
				models = append(models, lowered)
			}
		}

		return StartsWith(models, word), nil
	}
}

func GPUCounts(workload api.Workload) completion.Completer {
	return func(word string, ctx *completion.Context) ([]string, error) {
		rates, err := computeRates(ctx, workload, api.ComputeGPU)
		if err != nil {
			return nil, err
		}

		var counts []int

		for _, rate := range rates {
			if rate.GPUCount != nil {
				counts = append(counts, *rate.GPUCount)
			}
		}

		return StartsWith(numbers(counts), word), nil
	}
}

func CPUCores(workload api.Workload) completion.Completer {
	return func(word string, ctx *completion.Context) ([]string, error) {
		rates, err := computeRates(ctx, workload, api.ComputeCPU)
		if err != nil {
			return nil, err
		}

		var cores []int

		for _, rate := range rates {
			if rate.Resources != nil {
				cores = append(cores, rate.Resources.CPUCores)
			}
		}

		return StartsWith(numbers(cores), word), nil
	}
}

// selectCompute asks for the compute offer when no compute flags were given.
func selectCompute(prices []api.PriceRate, ctx *output.Context) (string, error) {
	kindOptions := []output.Option{{Value: "gpu", Label: "GPU"}, {Value: "cpu", Label: "CPU"}}
	kind, err := ctx.Prompt.Select("What compute do you need?", kindOptions, "gpu")

	if err != nil {
		return "", PromptFailure(ctx, err)
	}

	if kind == "cpu" {
		offers := cpuOffers(prices)

		if len(offers) == 0 {
			return "", errs.New("No CPU offer available. Run featherless-cloud pricing list.")
		}

		var cores []int

		for _, rate := range offers {
			cores = append(cores, rate.Resources.CPUCores)
		}

		var coreOptions []output.Option

		for _, count := range numbers(cores) {
			coreOptions = append(coreOptions, output.Option{Value: count, Label: count + " vCPU"})
		}

		chosen, err := ctx.Prompt.Select("How many vCPU cores?", coreOptions, "")

		if err != nil {
			return "", PromptFailure(ctx, err)
		}

		return resolveCPUSKU(prices, &chosen)
	}

	countOptions := []output.Option{
		{Value: "1", Label: "1 GPU"},
		{Value: "2", Label: "2 GPUs"},
		{Value: "4", Label: "4 GPUs"},
		{Value: "8", Label: "8 GPUs"},
	}

	count, err := ctx.Prompt.Select("How many GPUs?", countOptions, "1")

	if err != nil {
		return "", PromptFailure(ctx, err)
	}

	var offers []api.PriceRate

	for _, rate := range prices {
		if isGPUOffer(rate, ParseNumber(count)) {
			offers = append(offers, rate)
		}
	}

	if len(offers) == 0 {
		return "", errs.Newf("No GPU offer with %s GPU(s). Run featherless-cloud pricing list.", count)
	}

	if len(offers) == 1 {
		return offers[0].SKU, nil
	}

	offerOptions := make([]output.Option, len(offers))

	for i, rate := range offers {
		offerOptions[i] = output.Option{Value: rate.SKU, Label: rate.DisplayName}
	}

	sku, err := ctx.Prompt.Select("Which GPU offer?", offerOptions, "")

	if err != nil {
		return "", PromptFailure(ctx, err)
	}

	return sku, nil
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
	var fields ImageFields

	if opts.Registry != "" && opts.Image == "" && !hasTemplate {
		return fields, errs.New("--registry applies only with --image or a template.")
	}

	if opts.Image != "" {
		reference := strings.TrimSpace(opts.Image)

		if UTF16Length(reference) < 3 {
			return fields, errs.New("Enter a container image.")
		}

		fields.ContainerImage = reference
	}

	if opts.Registry != "" {
		id, err := ResolveCredentialRef(credentials, opts.Registry)
		if err != nil {
			return fields, err
		}

		fields.RegistryCredentialID = id
	}

	if !hasTemplate && fields.ContainerImage == "" {
		return fields, errs.New("Pass --image, --template or --template-id.")
	}

	return fields, nil
}

// ImageChoice is the template, or else the container image, picked at the prompt.
type ImageChoice struct {
	Template       *api.Template
	ContainerImage string
}

// SelectImage asks for a template when neither a template nor --image was given.
func SelectImage(templates []api.Template, ctx *output.Context) (ImageChoice, error) {
	if len(templates) == 0 {
		validate := func(value string) error {
			if UTF16Length(strings.TrimSpace(value)) >= 3 {
				return nil
			}

			return errs.New("Enter a container image.")
		}

		image, err := ctx.Prompt.Text("No templates are available. Enter a container image:", "", "", validate)

		if err != nil {
			return ImageChoice{}, PromptFailure(ctx, err)
		}

		return ImageChoice{ContainerImage: strings.TrimSpace(image)}, nil
	}

	options := make([]output.Option, len(templates))

	for i, template := range templates {
		label := TemplateReference(template, templates)

		if template.Official {
			label = template.Name + " " + template.Version.Version
		}

		options[i] = output.Option{Value: template.ID, Label: label}
	}

	id, err := ctx.Prompt.Select("Which template?", options, "")

	if err != nil {
		return ImageChoice{}, PromptFailure(ctx, err)
	}

	index := slices.IndexFunc(templates, func(template api.Template) bool { return template.ID == id })

	return ImageChoice{Template: &templates[index]}, nil
}

var visibilityOrder = []api.TemplateVisibility{api.VisibilityPrivate, api.VisibilityOrganization, api.VisibilityPublic}

// TemplateReference is what to type for a template: its name, qualified only
// when one closer to you has the same name. A bare name finds your template,
// then your organization's, then an official one.
func TemplateReference(template api.Template, templates []api.Template) string {
	rank := slices.Index(visibilityOrder, template.Visibility)

	hasCloser := slices.ContainsFunc(templates, func(other api.Template) bool {
		return other.Slug == template.Slug && slices.Index(visibilityOrder, other.Visibility) < rank
	})

	if !hasCloser {
		return template.Slug
	}

	if template.Official {
		return "featherless/" + template.Slug
	}

	return "org/" + template.Slug
}

// ResolveTemplateRef finds --template: an id, a name, org/<name>,
// featherless/<name>, or an official template's display name.
func ResolveTemplateRef(templates []api.Template, ref string) (api.Template, error) {
	needle := strings.ToLower(ref)

	for _, template := range templates {
		if template.ID == needle || template.ShortID == needle || template.Version.ID == needle {
			return template, nil
		}
	}

	scope, slug := "", needle

	if separator := strings.Index(needle, "/"); separator > 0 {
		scope = needle[:separator]
		slug = needle[separator+1:]
	}

	index := -1

	switch scope {
	case "org":
		index = slices.IndexFunc(templates, func(template api.Template) bool {
			return template.Visibility == api.VisibilityOrganization && template.Slug == slug
		})
	case "featherless":
		index = slices.IndexFunc(templates, func(template api.Template) bool {
			return template.Official && template.Slug == slug
		})
	default:
		for _, visibility := range visibilityOrder {
			index = slices.IndexFunc(templates, func(template api.Template) bool {
				return template.Visibility == visibility && template.Slug == slug
			})

			if index >= 0 {
				break
			}
		}
	}

	if index >= 0 {
		return templates[index], nil
	}

	// An official template's display name, as `--template pytorch` has always worked.
	var exact, partial []api.Template

	for _, template := range templates {
		name := strings.ToLower(template.Name)

		if template.Official && name == needle {
			exact = append(exact, template)
		}

		if template.Official && strings.Contains(name, needle) {
			partial = append(partial, template)
		}
	}

	candidates := partial

	if len(exact) > 0 {
		candidates = exact
	}

	if len(candidates) == 1 {
		return candidates[0], nil
	}

	if len(candidates) > 1 {
		names := make([]string, len(candidates))

		for i, template := range candidates {
			names[i] = TemplateReference(template, templates)
		}

		return api.Template{}, errs.Newf("Multiple templates match \"%s\" (%s). Be more specific.", ref, strings.Join(names, ", "))
	}

	return api.Template{}, errs.Newf("No template matching \"%s\". List them with featherless-cloud templates list.", ref)
}

// SplitTemplateVersion reads featherless/rocm-pytorch:2.11.0 as an official
// template's version; template names never hold a colon.
func SplitTemplateVersion(ref string) (name, version string) {
	separator := strings.Index(ref, ":")

	if separator > 0 {
		return ref[:separator], ref[separator+1:]
	}

	return ref, ""
}

// CheckTemplateVersion accepts only a version the official template lists.
func CheckTemplateVersion(template api.Template, version string) (string, error) {
	if version == "" {
		return "", nil
	}

	if !template.Official {
		return "", errs.Newf("Only official templates have versions, and %s is not one.", template.Name)
	}

	versions := make([]string, len(template.Versions))

	for i, entry := range template.Versions {
		versions[i] = entry.Version
	}

	if !slices.Contains(versions, version) {
		return "", errs.Newf("%s has no version %s. Versions: %s.", template.Name, version, strings.Join(versions, ", "))
	}

	return version, nil
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
	if opts.Template != "" && opts.TemplateID != "" {
		return nil, errs.New("Choose either --template or --template-id.")
	}

	var template *api.Template
	version := opts.TemplateVersion

	if opts.TemplateID != "" {
		found, err := client.Template(ctx, opts.TemplateID)
		if err != nil {
			return nil, err
		}

		template = &found
	}

	if opts.Template != "" {
		ref, refVersion := SplitTemplateVersion(opts.Template)

		if refVersion != "" && opts.TemplateVersion != "" {
			return nil, errs.New("Give the version once: --template name:version or --template-version.")
		}

		if refVersion != "" {
			version = refVersion
		}

		templates, err := client.Templates(ctx)
		if err != nil {
			return nil, err
		}

		found, err := ResolveTemplateRef(templates, ref)
		if err != nil {
			return nil, err
		}

		template = &found
	}

	if template == nil {
		if opts.TemplateVersion != "" {
			return nil, errs.New("--template-version needs --template or --template-id.")
		}

		return nil, nil
	}

	checked, err := CheckTemplateVersion(*template, version)
	if err != nil {
		return nil, err
	}

	return &TemplateChoice{Template: *template, Version: checked}, nil
}

// Templates completes what to type for each enabled template.
func Templates(word string, ctx *completion.Context) ([]string, error) {
	all, err := listTemplates(ctx)
	if err != nil {
		return nil, err
	}

	usable := slices.DeleteFunc(all, func(template api.Template) bool { return !template.Enabled })
	references := make([]string, len(usable))

	for i, template := range usable {
		references[i] = TemplateReference(template, usable)
	}

	return StartsWith(references, word), nil
}

func TemplateIDs(word string, ctx *completion.Context) ([]string, error) {
	all, err := listTemplates(ctx)
	if err != nil {
		return nil, err
	}

	ids := make([]string, len(all))

	for i, template := range all {
		ids[i] = template.ShortID
	}

	return StartsWith(ids, word), nil
}

func listTemplates(ctx *completion.Context) ([]api.Template, error) {
	cloud, err := ctx.Cloud()
	if err != nil {
		return nil, err
	}

	return cloud.Templates(context.Background())
}

// VolumeFlag is one --volume NAME:PATH.
type VolumeFlag struct {
	Volume    string
	MountPath string
}

func ParseVolumeFlags(values []string) ([]VolumeFlag, error) {
	entries := []VolumeFlag{}

	for _, entry := range values {
		separator := strings.LastIndex(entry, ":")

		if separator < 1 || separator == len(entry)-1 {
			return nil, errs.Newf("--volume expects NAME:PATH, got \"%s\"", entry)
		}

		entries = append(entries, VolumeFlag{Volume: entry[:separator], MountPath: entry[separator+1:]})
	}

	return entries, nil
}

func ParseDisk(value string) (int, error) {
	disk := ParseNumber(value)

	if !IsInteger(disk) || disk < 20 || disk > 2000 {
		return 0, errs.New("Container disk must be between 20 and 2000 GB.")
	}

	return int(disk), nil
}

// ComputeFlagsGiven reports whether any compute flag was typed, so a
// template's compute gives way.
func ComputeFlagsGiven(cmd *cobra.Command) bool {
	flags := cmd.Flags()

	return flags.Changed("compute-type") || flags.Changed("gpu-type") || flags.Changed("gpu-count") || flags.Changed("cpu-cores")
}

func ResolveCredentialRef(credentials []api.RegistryCredential, ref string) (string, error) {
	if UUIDPattern.MatchString(ref) {
		for _, credential := range credentials {
			if credential.ID == strings.ToLower(ref) {
				return credential.ID, nil
			}
		}
	}

	needle := strings.ToLower(ref)

	for _, credential := range credentials {
		if strings.ToLower(credential.Name) == needle {
			return credential.ID, nil
		}
	}

	return "", errs.Newf("No registry key matching \"%s\". List them with featherless-cloud registry-keys list.", ref)
}

func Registries(word string, ctx *completion.Context) ([]string, error) {
	cloud, err := ctx.Cloud()
	if err != nil {
		return nil, err
	}

	credentials, err := cloud.RegistryCredentials(context.Background())
	if err != nil {
		return nil, err
	}

	names := make([]string, len(credentials))

	for i, credential := range credentials {
		names[i] = credential.Name
	}

	return StartsWith(names, word), nil
}

func ResolveSSHKeyRef(keys []api.SSHKey, ref string) (api.SSHKey, error) {
	for _, key := range keys {
		if key.ID == strings.ToLower(ref) {
			return key, nil
		}
	}

	needle := strings.ToLower(ref)
	var matches []api.SSHKey

	for _, key := range keys {
		if strings.ToLower(key.Name) == needle {
			matches = append(matches, key)
		}
	}

	if len(matches) == 1 {
		return matches[0], nil
	}

	if len(matches) > 1 {
		return api.SSHKey{}, errs.Newf("Several SSH keys are named \"%s\". Use the ID from featherless-cloud ssh-keys list.", ref)
	}

	return api.SSHKey{}, errs.Newf("No SSH key matching \"%s\". List them with featherless-cloud ssh-keys list.", ref)
}

func SSHKeys(word string, ctx *completion.Context) ([]string, error) {
	cloud, err := ctx.Cloud()
	if err != nil {
		return nil, err
	}

	keys, err := cloud.SSHKeys(context.Background())
	if err != nil {
		return nil, err
	}

	names := make([]string, len(keys))

	for i, key := range keys {
		names[i] = key.Name
	}

	return StartsWith(names, word), nil
}

// FindVolume finds a volume by uuid, exact name, or unique substring, in any state.
func FindVolume(volumes []api.NetworkVolume, ref string) (api.NetworkVolume, error) {
	if UUIDPattern.MatchString(ref) {
		for _, volume := range volumes {
			if volume.ID == strings.ToLower(ref) {
				return volume, nil
			}
		}
	}

	var exact, partial []api.NetworkVolume
	needle := strings.ToLower(ref)

	for _, volume := range volumes {
		if volume.Name == ref {
			exact = append(exact, volume)
		}

		if strings.Contains(strings.ToLower(volume.Name), needle) {
			partial = append(partial, volume)
		}
	}

	if len(exact) == 1 {
		return exact[0], nil
	}

	if len(partial) == 1 {
		return partial[0], nil
	}

	return api.NetworkVolume{}, errs.Newf("No network volume matching \"%s\". Run featherless-cloud volume ls.", ref)
}

// ResolveVolume returns the id of an attachable (available or in use) volume.
func ResolveVolume(volumes []api.NetworkVolume, ref string) (string, error) {
	volume, err := FindVolume(volumes, ref)
	if err != nil {
		return "", err
	}

	if volume.State != api.VolumeAvailable && volume.State != api.VolumeInUse {
		return "", errs.Newf("Volume \"%s\" is %s. Wait for it to become available.", volume.Name, volume.State)
	}

	return volume.ID, nil
}

func Volumes(word string, ctx *completion.Context) ([]string, error) {
	cloud, err := ctx.Cloud()
	if err != nil {
		return nil, err
	}

	volumes, err := cloud.ListVolumes(context.Background())
	if err != nil {
		return nil, err
	}

	names := make([]string, len(volumes))

	for i, volume := range volumes {
		names[i] = volume.Name
	}

	return StartsWith(names, word), nil
}

// FindInstance finds an instance by uuid, exact name, or unique substring, in any state.
func FindInstance(instances []api.Instance, ref string) (api.Instance, error) {
	if UUIDPattern.MatchString(ref) {
		for _, instance := range instances {
			if instance.ID == strings.ToLower(ref) {
				return instance, nil
			}
		}
	}

	var exact, partial []api.Instance
	needle := strings.ToLower(ref)

	for _, instance := range instances {
		if instance.Name == ref {
			exact = append(exact, instance)
		}

		if strings.Contains(strings.ToLower(instance.Name), needle) {
			partial = append(partial, instance)
		}
	}

	ambiguous := func(matches []api.Instance) error {
		names := make([]string, len(matches))

		for i, match := range matches {
			names[i] = match.Name
		}

		return errs.Newf("Multiple instances match \"%s\": %s. Use the exact name or short id.", ref, strings.Join(names, ", "))
	}

	switch {
	case len(exact) > 1:
		return api.Instance{}, ambiguous(exact)
	case len(exact) == 1:
		return exact[0], nil
	case len(partial) > 1:
		return api.Instance{}, ambiguous(partial)
	case len(partial) == 1:
		return partial[0], nil
	}

	return api.Instance{}, errs.Newf("No instance matching \"%s\". Run featherless-cloud instance list.", ref)
}

// ResolveInstanceID turns an instance reference (uuid, short id, name, unique
// substring) into the id the API expects.
func ResolveInstanceID(ctx context.Context, client api.Cloud, ref string) (string, error) {
	if UUIDPattern.MatchString(ref) || shortIDPattern.MatchString(ref) {
		return ref, nil
	}

	var all []api.Instance

	for page := 1; ; page++ {
		list, err := client.ListInstances(ctx, page, 100)
		if err != nil {
			return "", err
		}

		all = append(all, list.Items...)

		if page >= list.Pagination.TotalPages {
			break
		}
	}

	instance, err := FindInstance(all, ref)
	if err != nil {
		return "", err
	}

	return instance.ID, nil
}

// Instances completes instance names and short ids.
func Instances(word string, ctx *completion.Context) ([]string, error) {
	cloud, err := ctx.Cloud()
	if err != nil {
		return nil, err
	}

	list, err := cloud.ListInstances(context.Background(), 1, 100)
	if err != nil {
		return nil, err
	}

	var names []string

	for _, instance := range list.Items {
		for _, name := range []string{instance.Name, instance.ShortID} {
			if !slices.Contains(names, name) {
				names = append(names, name)
			}
		}
	}

	return StartsWith(names, word), nil
}

// Jobs completes Run Job short ids; job commands take a uuid or short id.
func Jobs(word string, ctx *completion.Context) ([]string, error) {
	cloud, err := ctx.Cloud()
	if err != nil {
		return nil, err
	}

	list, err := cloud.ListJobs(context.Background(), 1, 100)
	if err != nil {
		return nil, err
	}

	ids := make([]string, len(list.Items))

	for i, job := range list.Items {
		ids[i] = job.ShortID
	}

	return StartsWith(ids, word), nil
}

var (
	nameAdjectives = []string{"amber", "bold", "bright", "calm", "clever", "cosmic", "eager", "lucky", "rapid", "steady"}
	nameNouns      = []string{"comet", "falcon", "forge", "nebula", "orbit", "phoenix", "rocket", "summit", "tiger", "vector"}
)

// RandomInstanceName is a friendly name, as the console launcher picks.
func RandomInstanceName() string {
	var random [12]byte
	_, _ = rand.Read(random[:])
	adjective := binary.LittleEndian.Uint32(random[0:4]) % uint32(len(nameAdjectives))
	noun := binary.LittleEndian.Uint32(random[4:8]) % uint32(len(nameNouns))
	suffix := binary.LittleEndian.Uint32(random[8:12]) % 1000

	return fmt.Sprintf("%s-%s-%03d", nameAdjectives[adjective], nameNouns[noun], suffix)
}

// AssertInstanceName applies the naming rule the API enforces.
func AssertInstanceName(name string) error {
	if !instanceName.MatchString(name) {
		return errs.New("Instance names must be lowercase letters, digits, and hyphens (max 63, no leading/trailing hyphen).")
	}

	return nil
}

// RequireDeletion confirms a destructive operation; non-interactive runs need --yes.
func RequireDeletion(ctx *output.Context, label string, yes bool) error {
	if ctx.Mode == output.Interactive && !yes {
		isConfirmed, err := ctx.Prompt.Confirm("Delete "+label+"? This cannot be undone.", true)

		if err != nil {
			return PromptFailure(ctx, err)
		}

		if !isConfirmed {
			return Cancelled(ctx)
		}

		return nil
	}

	if !yes {
		return ctx.Fatal("Refusing to delete " + label + " without --yes.")
	}

	return nil
}

func Profiles(word string, ctx *completion.Context) ([]string, error) {
	cfg, err := LoadConfig()
	if err != nil {
		return nil, err
	}

	return StartsWith(cfg.Profiles.Names(), word), nil
}

var signalsTaken atomic.Int32

// TakeSignals marks that a command handles Ctrl-C itself (exec detaching from
// a Run Job, ssh passing it on) until release is called.
func TakeSignals() (release func()) {
	signalsTaken.Add(1)
	var once sync.Once

	return func() {
		once.Do(func() { signalsTaken.Add(-1) })
	}
}

// SignalsTaken reports whether a command is handling Ctrl-C itself.
func SignalsTaken() bool {
	return signalsTaken.Load() > 0
}
