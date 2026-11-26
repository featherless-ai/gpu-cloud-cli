package shared

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"featherless.ai/featherless-cloud/internal/api"
	"featherless.ai/featherless-cloud/internal/errs"
	"featherless.ai/featherless-cloud/internal/output"
)

func swap[T any](t *testing.T, target *T, value T) {
	t.Helper()
	previous := *target
	*target = value
	t.Cleanup(func() { *target = previous })
}

// fakeCloud answers only the calls a test sets; any other call panics.
type fakeCloud struct {
	api.Cloud
	listInstances       func(page, limit int) (api.InstanceList, error)
	listJobs            func(page, limit int) (api.RunJobList, error)
	listVolumes         func() ([]api.NetworkVolume, error)
	templates           func() ([]api.Template, error)
	registryCredentials func() ([]api.RegistryCredential, error)
	pricing             func(workload api.Workload) ([]api.PriceRate, error)
}

func (f *fakeCloud) ListInstances(ctx context.Context, page, limit int) (api.InstanceList, error) {
	return f.listInstances(page, limit)
}

func (f *fakeCloud) ListJobs(ctx context.Context, page, limit int) (api.RunJobList, error) {
	return f.listJobs(page, limit)
}

func (f *fakeCloud) ListVolumes(ctx context.Context) ([]api.NetworkVolume, error) {
	return f.listVolumes()
}

func (f *fakeCloud) Templates(ctx context.Context) ([]api.Template, error) {
	return f.templates()
}

func (f *fakeCloud) RegistryCredentials(ctx context.Context) ([]api.RegistryCredential, error) {
	return f.registryCredentials()
}

func (f *fakeCloud) Pricing(ctx context.Context, workload api.Workload) ([]api.PriceRate, error) {
	return f.pricing(workload)
}

// testContext is a plain-mode context whose output the test reads.
func testContext() (*output.Context, *bytes.Buffer, *bytes.Buffer) {
	var stdout, stderr bytes.Buffer
	ctx := output.New(output.Plain)
	ctx.Stdout = &stdout
	ctx.Stderr = &stderr

	return ctx, &stdout, &stderr
}

// wantError checks err is set and its message contains want, as vitest's toThrow(string) does.
func wantError(t *testing.T, err error, want string) {
	t.Helper()

	if err == nil {
		t.Errorf("no error, want %q", want)
		return
	}

	if !strings.Contains(err.Error(), want) {
		t.Errorf("error %q, want it to contain %q", err.Error(), want)
	}
}

func gpuRate(sku string, gpuCount int, gpuModel string) api.PriceRate {
	return api.PriceRate{
		SKU:                      sku,
		DisplayName:              fmt.Sprintf("%dx %s", gpuCount, gpuModel),
		ResourceType:             api.ResourceTypeInstanceGPU,
		Unit:                     api.UnitGPUHour,
		GPUCount:                 new(gpuCount),
		UnitPriceMicros:          "1000000",
		CompareAtUnitPriceMicros: nil,
		Currency:                 "usd",
		EffectiveAt:              "2026-01-01T00:00:00Z",
		ExpiresAt:                nil,
		Resources:                &api.Resources{GPUModel: new(gpuModel), GPUMemoryGiB: new(192), CPUCores: 16, MemoryGiB: 256},
	}
}

func cpuRate(sku string, cpuCores int) api.PriceRate {
	return api.PriceRate{
		SKU:                      sku,
		DisplayName:              fmt.Sprintf("%d vCPU", cpuCores),
		ResourceType:             api.ResourceTypeInstanceCPU,
		Unit:                     api.UnitVCPUHour,
		GPUCount:                 nil,
		UnitPriceMicros:          "30000",
		CompareAtUnitPriceMicros: nil,
		Currency:                 "usd",
		EffectiveAt:              "2026-01-01T00:00:00Z",
		ExpiresAt:                nil,
		Resources:                &api.Resources{GPUModel: nil, GPUMemoryGiB: nil, CPUCores: cpuCores, MemoryGiB: 32},
	}
}

var testPrices = []api.PriceRate{
	gpuRate("gpu.mi325x.1x", 1, "MI325X"),
	gpuRate("gpu.mi325x.2x", 2, "MI325X"),
	gpuRate("gpu.h100.1x", 1, "H100"),
	cpuRate("cpu.small", 4),
	cpuRate("cpu.large", 16),
}

var notLetters = regexp.MustCompile(`[^a-z]`)

func templateFixture(id, slug, name string, visibility api.TemplateVisibility) api.Template {
	public := visibility == api.VisibilityPublic
	letters := notLetters.ReplaceAllString(slug, "")
	var owner *api.TemplateOwner

	if !public {
		owner = &api.TemplateOwner{ID: "u1", Name: "Ada"}
	}

	return api.Template{
		ID:              id,
		Name:            name,
		Description:     "",
		ImageRepository: nil,
		Enabled:         true,
		ComputeTypes:    []api.ComputeType{api.ComputeGPU},
		Version: api.CatalogVersion{
			ID: id, Version: "", Tag: nil, State: "published", Image: "ghcr.io/acme/app:1", StartupCommand: "",
		},
		ShortID:              (letters + strings.Repeat("b", 10))[:10],
		Slug:                 slug,
		Official:             public,
		Visibility:           visibility,
		Owner:                owner,
		CanEdit:              !public,
		Image:                "ghcr.io/acme/app:1",
		ImageSource:          api.ImageSourceCustom,
		RegistryCredentialID: nil,
		ComputeSKU:           nil,
		EphemeralDiskGiB:     nil,
		NetworkVolumeMounts:  []api.NetworkVolumeMount{},
		Command:              []string{},
		Environment:          []api.EnvVar{},
		SecretNames:          []string{},
		PublicPorts:          []int{},
		AutoTmux:             nil,
		Tags:                 []string{},
		Versions:             []api.TemplateVersionOption{},
		GPUVendors:           []api.GPUVendor{api.VendorAMD, api.VendorNVIDIA},
	}
}

var (
	pytorchTemplate     = templateFixture("11111111-1111-4111-8111-111111111111", "rocm-pytorch", "PyTorch (ROCm)", api.VisibilityPublic)
	mineTemplate        = templateFixture("33333333-3333-4333-8333-333333333331", "trainer", "trainer", api.VisibilityPrivate)
	sharedTemplate      = templateFixture("33333333-3333-4333-8333-333333333332", "trainer", "trainer", api.VisibilityOrganization)
	sharedServeTemplate = templateFixture("33333333-3333-4333-8333-333333333333", "serve", "serve", api.VisibilityOrganization)
	myPytorchTemplate   = templateFixture("33333333-3333-4333-8333-333333333334", "rocm-pytorch", "rocm-pytorch", api.VisibilityPrivate)
	testTemplates       = []api.Template{pytorchTemplate, mineTemplate, sharedTemplate, sharedServeTemplate}
)

var testCredentials = []api.RegistryCredential{{
	ID:        "22222222-2222-4222-8222-222222222222",
	Name:      "Private Hub",
	Registry:  "registry.example.com",
	Username:  "robot",
	UpdatedAt: "2026-10-26T00:00:00.000Z",
}}

func volumeFixture() api.NetworkVolume {
	return api.NetworkVolume{
		ID:          "33333333-3333-4333-8333-333333333333",
		Name:        "data",
		CapacityGiB: 100,
		State:       api.VolumeAvailable,
		Attachments: []api.VolumeAttachment{},
		Pricing: api.VolumePricing{
			UnitPriceMicrosPerGiBMonth: "50000",
			EstimatedMicrosPerMonth:    "5000000",
			BillingActive:              true,
		},
		LastError: nil,
		CreatedAt: "2026-01-01T00:00:00Z",
		UpdatedAt: "2026-01-01T00:00:00Z",
	}
}

func instanceFixture() api.Instance {
	return api.Instance{
		ID:                "55555555-5555-4555-8555-555555555555",
		ShortID:           "tmxbnd1234",
		Name:              "tmux-bundle-ubuntu",
		ComputeLabel:      "1 x MI325X",
		ComputeType:       api.ComputeGPU,
		GPUCount:          1,
		State:             api.InstanceRunning,
		StartupStage:      nil,
		CostMicrosPerHour: "1900000",
		EphemeralDiskGiB:  30,
		CreatedAt:         "2026-01-01T00:00:00Z",
	}
}

func datasetsInstance() api.Instance {
	instance := instanceFixture()
	instance.ID = "66666666-6666-4666-8666-666666666666"
	instance.ShortID = "dtspr0d012"
	instance.Name = "datasets"

	return instance
}

// clientWithPages lists one instance per page.
func clientWithPages(pages ...api.Instance) api.Cloud {
	return &fakeCloud{
		listInstances: func(page, limit int) (api.InstanceList, error) {
			items := []api.Instance{}

			if page-1 < len(pages) {
				items = []api.Instance{pages[page-1]}
			}

			pagination := api.Pagination{Page: page, PageSize: 1, TotalItems: len(pages), TotalPages: len(pages)}

			return api.InstanceList{Items: items, Pagination: pagination}, nil
		},
	}
}

func TestParsePortsReadsRepeatedPortsAndNoneAloneAsNoPorts(t *testing.T) {
	cases := []struct {
		values []string
		want   []int
	}{
		{[]string{"8000", "7860"}, []int{8000, 7860}},
		{[]string{"none"}, []int{}},
		{nil, []int{}},
	}

	for _, c := range cases {
		got, err := ParsePorts(c.values)

		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("ParsePorts(%q) = %#v, %v; want %#v", c.values, got, err, c.want)
		}
	}
}

func TestParsePortsRefuses(t *testing.T) {
	cases := [][]string{{"0"}, {"65536"}, {"8000.5"}, {"http"}, {"8000", "8000"}, {"none", "8000"}}

	for _, values := range cases {
		t.Run(strings.Join(values, " "), func(t *testing.T) {
			_, err := ParsePorts(values)

			if err == nil || !errs.IsCLI(err) {
				t.Errorf("ParsePorts(%q) error = %v, want a CLI error", values, err)
			}
		})
	}
}

func TestParseRepeatableSplitsOnTheFirstEquals(t *testing.T) {
	got, err := ParseRepeatable([]string{"A=1", "B=x=y=z"}, "env")
	want := []api.EnvVar{{Name: "A", Value: "1"}, {Name: "B", Value: "x=y=z"}}

	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("got %#v, %v; want %#v", got, err, want)
	}
}

func TestParseRepeatableRejectsEntriesWithoutAName(t *testing.T) {
	_, err := ParseRepeatable([]string{"novalue"}, "env")
	wantError(t, err, `--env expects NAME=VALUE, got "novalue"`)

	_, err = ParseRepeatable([]string{"=x"}, "env")
	wantError(t, err, "expects NAME=VALUE")
}

func TestParseRepeatableTreatsMissingFlagsAsNoEntries(t *testing.T) {
	got, err := ParseRepeatable(nil, "secret")

	if err != nil || !reflect.DeepEqual(got, []api.EnvVar{}) {
		t.Errorf("got %#v, %v; want no entries", got, err)
	}
}

func TestToEnvironmentPassesValidUniqueNamesThrough(t *testing.T) {
	pairs := []api.EnvVar{{Name: "HF_TOKEN", Value: "x"}, {Name: "_A2", Value: ""}}
	got, err := ToEnvironment(pairs)

	if err != nil || !reflect.DeepEqual(got, pairs) {
		t.Errorf("got %#v, %v; want %#v", got, err, pairs)
	}
}

func TestToEnvironmentRejectsInvalidCharactersWithTheConsoleCopy(t *testing.T) {
	_, err := ToEnvironment([]api.EnvVar{{Name: "9BAD", Value: "x"}})
	wantError(t, err, "Variable names must be valid and unique.")
}

func TestToEnvironmentRejectsDuplicatesWithTheConsoleCopy(t *testing.T) {
	_, err := ToEnvironment([]api.EnvVar{{Name: "A", Value: "1"}, {Name: "A", Value: "2"}})
	wantError(t, err, "Variable names must be valid and unique.")
}

func TestValidateMountsAcceptsDisjointAbsolutePaths(t *testing.T) {
	err := ValidateMounts([]api.NetworkVolumeMount{
		{NetworkVolumeID: "a", MountPath: "/workspace"},
		{NetworkVolumeID: "b", MountPath: "/data/models"},
	})

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestValidateMountsRejectsNestedPaths(t *testing.T) {
	err := ValidateMounts([]api.NetworkVolumeMount{
		{NetworkVolumeID: "a", MountPath: "/workspace"},
		{NetworkVolumeID: "b", MountPath: "/workspace/data"},
	})

	wantError(t, err, "Mount paths must not overlap.")
}

func TestValidateMountsRejectsDoubleSlashAndTrailingSlashes(t *testing.T) {
	err := ValidateMounts([]api.NetworkVolumeMount{{NetworkVolumeID: "a", MountPath: "//workspace"}})
	wantError(t, err, "Use an absolute mount path such as /workspace.")

	err = ValidateMounts([]api.NetworkVolumeMount{{NetworkVolumeID: "a", MountPath: "/workspace/"}})
	wantError(t, err, "Use an absolute mount path such as /workspace.")
}

func TestValidateMountsRejectsDuplicateVolumeIDs(t *testing.T) {
	err := ValidateMounts([]api.NetworkVolumeMount{
		{NetworkVolumeID: "a", MountPath: "/one"},
		{NetworkVolumeID: "a", MountPath: "/two"},
	})

	wantError(t, err, "Each network volume can be mounted only once.")
}

func wantSKU(t *testing.T, got string, err error, want string) {
	t.Helper()

	if err != nil || got != want {
		t.Errorf("got %q, %v; want %q", got, err, want)
	}
}

func TestResolveComputePicksTheUniqueGPUOfferForTypeAndCount(t *testing.T) {
	ctx, _, _ := testContext()
	opts := ComputeOptions{ComputeType: api.ComputeGPU, GPUType: new("mi325x"), GPUCount: new("2")}
	sku, err := ResolveCompute(testPrices, opts, ctx)

	wantSKU(t, sku, err, "gpu.mi325x.2x")
}

func TestResolveComputeListsCandidatesWhenTheGPUTypeIsAmbiguous(t *testing.T) {
	ctx, _, _ := testContext()
	ambiguous := []api.PriceRate{gpuRate("gpu.a.1x", 1, "A100"), gpuRate("gpu.b.1x", 1, "A100")}
	_, err := ResolveCompute(ambiguous, ComputeOptions{ComputeType: api.ComputeGPU, GPUType: new("a100")}, ctx)

	wantError(t, err, "Multiple GPU offers match (1x A100, 1x A100)")
}

func TestResolveComputeRejectsUnsupportedGPUCounts(t *testing.T) {
	ctx, _, _ := testContext()
	_, err := ResolveCompute(testPrices, ComputeOptions{ComputeType: api.ComputeGPU, GPUCount: new("3")}, ctx)

	wantError(t, err, "--gpu-count must be 1, 2, 4, or 8")
}

func TestResolveComputePicksTheSmallestCPUOfferByDefault(t *testing.T) {
	ctx, _, _ := testContext()
	sku, err := ResolveCompute(testPrices, ComputeOptions{ComputeType: api.ComputeCPU}, ctx)

	wantSKU(t, sku, err, "cpu.small")
}

func TestResolveComputePicksTheRequestedCoreCount(t *testing.T) {
	ctx, _, _ := testContext()
	sku, err := ResolveCompute(testPrices, ComputeOptions{ComputeType: api.ComputeCPU, CPUCores: new("16")}, ctx)

	wantSKU(t, sku, err, "cpu.large")
}

func TestResolveComputeDefaultsToOneGPUInPlainMode(t *testing.T) {
	ctx, _, _ := testContext()
	plain := []api.PriceRate{gpuRate("gpu.mi325x.1x", 1, "MI325X")}
	sku, err := ResolveCompute(plain, ComputeOptions{ComputeType: api.ComputeGPU}, ctx)

	wantSKU(t, sku, err, "gpu.mi325x.1x")
}

func TestResolveComputeDefaultsABareCountToTheInServiceModelAmongPricedOffers(t *testing.T) {
	ctx, _, _ := testContext()

	catalog := []api.PriceRate{
		gpuRate("gpu.mi325x.1x", 1, "MI325X"),
		gpuRate("gpu.b300.1x", 1, "B300"),
		gpuRate("gpu.h100.1x", 1, "H100"),
	}

	sku, err := ResolveCompute(catalog, ComputeOptions{ComputeType: api.ComputeGPU}, ctx)

	wantSKU(t, sku, err, "gpu.mi325x.1x")
}

func wantImage(t *testing.T, got ImageFields, err error, want ImageFields) {
	t.Helper()

	if err != nil || got != want {
		t.Errorf("got %+v, %v; want %+v", got, err, want)
	}
}

func TestResolveImageReplacesATemplatesImageOrRegistryKey(t *testing.T) {
	fields, err := ResolveImage(testCredentials, ImageOptions{Image: "nginx:1"}, true)
	wantImage(t, fields, err, ImageFields{ContainerImage: "nginx:1"})

	fields, err = ResolveImage(testCredentials, ImageOptions{Registry: "Private Hub"}, true)
	wantImage(t, fields, err, ImageFields{RegistryCredentialID: testCredentials[0].ID})
}

func TestResolveImageRejectsRegistryWithoutAnImageOrATemplate(t *testing.T) {
	_, err := ResolveImage(testCredentials, ImageOptions{Registry: "Private Hub"}, false)
	wantError(t, err, "--registry applies only with --image or a template.")
}

func TestResolveImageResolvesAPrivateImageWithANamedCredential(t *testing.T) {
	opts := ImageOptions{Image: "registry.example.com/x:1", Registry: "private hub"}
	fields, err := ResolveImage(testCredentials, opts, false)
	want := ImageFields{ContainerImage: "registry.example.com/x:1", RegistryCredentialID: testCredentials[0].ID}

	wantImage(t, fields, err, want)
}

func TestResolveImageRejectsTooShortImageReferences(t *testing.T) {
	_, err := ResolveImage(nil, ImageOptions{Image: "x:"}, false)
	wantError(t, err, "Enter a container image.")
}

func TestResolveImageRequiresAnExplicitChoiceInPlainMode(t *testing.T) {
	_, err := ResolveImage(nil, ImageOptions{}, false)
	wantError(t, err, "Pass --image, --template or --template-id.")
}

func versionedTemplate() api.Template {
	template := templateFixture("11111111-1111-4111-8111-111111111119", "rocm-pytorch", "PyTorch (ROCm)", api.VisibilityPublic)
	template.Versions = []api.TemplateVersionOption{{Version: "2.12.0", Image: "r:2.12"}, {Version: "2.11.0", Image: "r:2.11"}}

	return template
}

func TestTemplateVersionsReadsAVersionAfterTheColonInATemplateReference(t *testing.T) {
	ref, version := SplitTemplateVersion("featherless/rocm-pytorch:2.11.0")

	if ref != "featherless/rocm-pytorch" || version != "2.11.0" {
		t.Errorf("got %q, %q", ref, version)
	}

	ref, version = SplitTemplateVersion("trainer")

	if ref != "trainer" || version != "" {
		t.Errorf("got %q, %q; want trainer and no version", ref, version)
	}
}

func TestTemplateVersionsAcceptsOnlyVersionsTheOfficialTemplateLists(t *testing.T) {
	versioned := versionedTemplate()

	version, err := CheckTemplateVersion(versioned, "2.11.0")

	if err != nil || version != "2.11.0" {
		t.Errorf("got %q, %v; want 2.11.0", version, err)
	}

	version, err = CheckTemplateVersion(versioned, "")

	if err != nil || version != "" {
		t.Errorf("got %q, %v; want no version", version, err)
	}

	_, err = CheckTemplateVersion(versioned, "9.9.9")
	wantError(t, err, "PyTorch (ROCm) has no version 9.9.9. Versions: 2.12.0, 2.11.0.")

	_, err = CheckTemplateVersion(mineTemplate, "1")
	wantError(t, err, "Only official templates have versions")
}

func wantTemplate(t *testing.T, templates []api.Template, ref string, want api.Template) {
	t.Helper()
	got, err := ResolveTemplateRef(templates, ref)

	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("ResolveTemplateRef(%q) = %s (%s), %v; want %s (%s)", ref, got.ID, got.Visibility, err, want.ID, want.Visibility)
	}
}

func TestResolveTemplateRefFindsYourTemplateFirstThenYourOrganizationsThenAnOfficialOne(t *testing.T) {
	withMine := append(append([]api.Template{}, testTemplates...), myPytorchTemplate)

	wantTemplate(t, testTemplates, "trainer", mineTemplate)
	wantTemplate(t, testTemplates, "serve", sharedServeTemplate)
	wantTemplate(t, testTemplates, "rocm-pytorch", pytorchTemplate)
	wantTemplate(t, withMine, "rocm-pytorch", myPytorchTemplate)
}

func TestResolveTemplateRefReachesAShadowedTemplateThroughItsNamespace(t *testing.T) {
	withMine := append(append([]api.Template{}, testTemplates...), myPytorchTemplate)

	wantTemplate(t, testTemplates, "org/trainer", sharedTemplate)
	wantTemplate(t, withMine, "featherless/rocm-pytorch", pytorchTemplate)

	_, err := ResolveTemplateRef(testTemplates, "org/rocm-pytorch")
	wantError(t, err, `No template matching "org/rocm-pytorch"`)
}

func TestResolveTemplateRefFindsATemplateByIDOrShortID(t *testing.T) {
	wantTemplate(t, testTemplates, strings.ToUpper(sharedTemplate.ID), sharedTemplate)
	wantTemplate(t, testTemplates, sharedServeTemplate.ShortID, sharedServeTemplate)
}

func TestResolveTemplateRefStillMatchesPartOfAnOfficialTemplatesDisplayName(t *testing.T) {
	wantTemplate(t, testTemplates, "PyTorch", pytorchTemplate)

	nightly := templateFixture("t9", "rocm-pytorch-nightly", "PyTorch nightly", api.VisibilityPublic)
	withNightly := append(append([]api.Template{}, testTemplates...), nightly)
	_, err := ResolveTemplateRef(withNightly, "pytorch")

	wantError(t, err, `Multiple templates match "pytorch"`)
}

func TestTemplateReferenceNamesATemplateBareUnlessACloserOneHasItsName(t *testing.T) {
	var got []string

	for _, template := range testTemplates {
		got = append(got, TemplateReference(template, testTemplates))
	}

	want := []string{"rocm-pytorch", "trainer", "org/trainer", "serve"}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}

	withMine := append(append([]api.Template{}, testTemplates...), myPytorchTemplate)

	if reference := TemplateReference(pytorchTemplate, withMine); reference != "featherless/rocm-pytorch" {
		t.Errorf("got %q, want featherless/rocm-pytorch", reference)
	}
}

func wantVolumeID(t *testing.T, volumes []api.NetworkVolume, ref, want string) {
	t.Helper()
	got, err := ResolveVolume(volumes, ref)

	if err != nil || got != want {
		t.Errorf("ResolveVolume(%q) = %q, %v; want %q", ref, got, err, want)
	}
}

func TestResolveVolumeMatchesExactNamesAndUniqueSubstrings(t *testing.T) {
	datasets := volumeFixture()
	datasets.ID = "44444444-4444-4444-8444-444444444444"
	datasets.Name = "datasets"
	volumes := []api.NetworkVolume{volumeFixture(), datasets}

	wantVolumeID(t, volumes, "data", volumes[0].ID)
	wantVolumeID(t, volumes, "sets", volumes[1].ID)
	wantVolumeID(t, volumes, volumes[0].ID, volumes[0].ID)
}

func TestResolveVolumeErrorsOnUnknownReferences(t *testing.T) {
	_, err := ResolveVolume([]api.NetworkVolume{volumeFixture()}, "missing")
	wantError(t, err, `No network volume matching "missing". List them with featherless-cloud volume ls, or create one with featherless-cloud volume create.`)
}

func TestResolveVolumeRefusesVolumesThatAreNotAttachable(t *testing.T) {
	provisioning := volumeFixture()
	provisioning.State = api.VolumeProvisioning
	_, err := ResolveVolume([]api.NetworkVolume{provisioning}, "data")

	wantError(t, err, `Volume "data" is provisioning. Wait for it to become available.`)
}

func wantInstance(t *testing.T, instances []api.Instance, ref string, want api.Instance) {
	t.Helper()
	got, err := FindInstance(instances, ref)

	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("FindInstance(%q) = %q, %v; want %q", ref, got.Name, err, want.Name)
	}
}

func TestFindInstanceMatchesExactNamesUniqueSubstringsAndUUIDs(t *testing.T) {
	found := instanceFixture()
	list := []api.Instance{found, datasetsInstance()}

	wantInstance(t, list, "tmux-bundle-ubuntu", found)
	wantInstance(t, list, "bundle", found)
	wantInstance(t, list, found.ID, found)
}

func TestFindInstanceListsEveryCandidateOnAnAmbiguousSubstring(t *testing.T) {
	trainA := instanceFixture()
	trainA.Name = "train-a"
	trainB := instanceFixture()
	trainB.ID = "66666666-6666-4666-8666-666666666666"
	trainB.ShortID = "trnnb00012"
	trainB.Name = "train-b"

	_, err := FindInstance([]api.Instance{trainA, trainB}, "train")
	wantError(t, err, `Multiple instances match "train": train-a, train-b. Use the exact name or short id.`)
}

func TestFindInstanceErrorsOnUnknownReferences(t *testing.T) {
	list := []api.Instance{instanceFixture()}

	_, err := FindInstance(list, "missing")
	wantError(t, err, `No instance matching "missing". Run featherless-cloud instance list.`)

	_, err = FindInstance(list, "99999999-9999-4999-8999-999999999999")
	wantError(t, err, `No instance matching "99999999-9999-4999-8999-999999999999". Run featherless-cloud instance list.`)
}

func TestResolveInstanceIDPassesUUIDAndShortIDReferencesThroughWithoutListing(t *testing.T) {
	client := &fakeCloud{
		listInstances: func(page, limit int) (api.InstanceList, error) {
			return api.InstanceList{}, errors.New("no call")
		},
	}

	uuid := instanceFixture().ID

	for _, ref := range []string{uuid, "tmxbnd1234"} {
		id, err := ResolveInstanceID(context.Background(), client, ref)

		if err != nil || id != ref {
			t.Errorf("ResolveInstanceID(%q) = %q, %v; want it unchanged", ref, id, err)
		}
	}
}

func TestResolveInstanceIDResolvesANameFromALaterPage(t *testing.T) {
	client := clientWithPages(instanceFixture(), datasetsInstance())
	id, err := ResolveInstanceID(context.Background(), client, "datasets")

	if err != nil || id != "66666666-6666-4666-8666-666666666666" {
		t.Errorf("got %q, %v", id, err)
	}
}

func TestResolveInstanceIDErrorsWhenNoPageMatches(t *testing.T) {
	client := clientWithPages(instanceFixture())
	_, err := ResolveInstanceID(context.Background(), client, "missing")

	if err == nil || !errs.IsCLI(err) {
		t.Errorf("error = %v, want a CLI error", err)
	}
}

func TestInstanceNamesGeneratesConsoleParityRandomNames(t *testing.T) {
	pattern := regexp.MustCompile(`^(amber|bold|bright|calm|clever|cosmic|eager|lucky|rapid|steady)-(comet|falcon|forge|nebula|orbit|phoenix|rocket|summit|tiger|vector)-\d{3}$`)

	if name := RandomInstanceName(); !pattern.MatchString(name) {
		t.Errorf("%q does not match %s", name, pattern)
	}
}

func TestInstanceNamesRejectsUppercaseAndUnderscores(t *testing.T) {
	wantError(t, AssertInstanceName("bad_name"), "lowercase letters, digits, and hyphens")
	wantError(t, AssertInstanceName("-lead"), "lowercase")

	if err := AssertInstanceName("good-name-1"); err != nil {
		t.Errorf("good-name-1: %v", err)
	}
}

func TestRequireDeletionRefusesInNonInteractiveModeWithoutYes(t *testing.T) {
	ctx, _, stderr := testContext()
	err := RequireDeletion(ctx, "instance abc", false)

	if code, ok := errs.ExitCode(err); !ok || code != 1 {
		t.Errorf("error = %v, want exit 1", err)
	}

	if !strings.Contains(stderr.String(), "Refusing to delete instance abc without --yes.") {
		t.Errorf("stderr = %q", stderr.String())
	}
}

func TestRequireDeletionProceedsWithYesWithoutPrompting(t *testing.T) {
	ctx, _, _ := testContext()

	if err := RequireDeletion(ctx, "volume v", true); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func sshKeysFixture() []api.SSHKey {
	return []api.SSHKey{
		{ID: "33333333-3333-4333-8333-333333333333", Name: "laptop", Fingerprint: "SHA256:a", CreatedAt: "2026-10-26T00:00:00.000Z"},
		{ID: "44444444-4444-4444-8444-444444444444", Name: "ci", Fingerprint: "SHA256:b", CreatedAt: "2026-10-26T00:00:00.000Z"},
		{ID: "55555555-5555-4555-8555-555555555555", Name: "ci", Fingerprint: "SHA256:c", CreatedAt: "2026-10-26T00:00:00.000Z"},
	}
}

func TestResolveSSHKeyRefMatchesANameCaseInsensitivelyOrAnID(t *testing.T) {
	keys := sshKeysFixture()

	key, err := ResolveSSHKeyRef(keys, "Laptop")

	if err != nil || key.ID != keys[0].ID {
		t.Errorf("Laptop: got %q, %v", key.ID, err)
	}

	key, err = ResolveSSHKeyRef(keys, "44444444-4444-4444-8444-444444444444")

	if err != nil || key.Fingerprint != "SHA256:b" {
		t.Errorf("by id: got %q, %v", key.Fingerprint, err)
	}
}

func TestResolveSSHKeyRefAsksForTheIDWhenANameIsShared(t *testing.T) {
	_, err := ResolveSSHKeyRef(sshKeysFixture(), "ci")
	wantError(t, err, `Several SSH keys are named "ci"`)
}

func TestResolveSSHKeyRefRejectsAnUnknownKey(t *testing.T) {
	_, err := ResolveSSHKeyRef(sshKeysFixture(), "nope")
	wantError(t, err, `No SSH key matching "nope"`)
}
