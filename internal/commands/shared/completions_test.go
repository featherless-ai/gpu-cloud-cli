package shared

import (
	"reflect"
	"testing"

	"featherless.ai/featherless-cloud/internal/api"
	"featherless.ai/featherless-cloud/internal/completion"
	"featherless.ai/featherless-cloud/internal/config"
)

// completerContext is a completer context whose client is cloud.
func completerContext(cloud api.Cloud, args ...string) *completion.Context {
	return &completion.Context{
		Args:  args,
		Cloud: func() (api.Cloud, error) { return cloud, nil },
	}
}

// wantCandidates compares candidates; no candidates and an empty list are the same.
func wantCandidates(t *testing.T, label string, got []string, err error, want []string) {
	t.Helper()

	if err != nil {
		t.Errorf("%s: %v", label, err)
		return
	}

	if len(got) == 0 && len(want) == 0 {
		return
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s = %q, want %q", label, got, want)
	}
}

var (
	datasetsVolume = api.NetworkVolume{ID: "11111111-2222-4333-8444-555555555555", Name: "datasets"}
	myDataVolume   = api.NetworkVolume{ID: "33333333-2222-4333-8444-555555555555", Name: "my data"}
	tmuxInstance   = api.Instance{ID: "44444444-2222-4333-8444-555555555555", ShortID: "tmxbnd1234", Name: "tmux-bundle-ubuntu"}
	datasetsProd   = api.Instance{ID: "55555555-2222-4333-8444-555555555555", ShortID: "dtspr0d012", Name: "datasets-prod"}
)

func TestSharedCompletersCompletesInstanceNamesAndShortIDsByCaseInsensitivePrefix(t *testing.T) {
	ctx := completerContext(&fakeCloud{
		listInstances: func(page, limit int) (api.InstanceList, error) {
			return api.InstanceList{Items: []api.Instance{tmuxInstance, datasetsProd}}, nil
		},
	})

	got, err := Instances("tm", ctx)
	wantCandidates(t, "tm", got, err, []string{"tmux-bundle-ubuntu", "tmxbnd1234"})

	got, err = Instances("TMUX-B", ctx)
	wantCandidates(t, "TMUX-B", got, err, []string{"tmux-bundle-ubuntu"})

	got, err = Instances("", ctx)
	wantCandidates(t, "everything", got, err, []string{"tmux-bundle-ubuntu", "tmxbnd1234", "datasets-prod", "dtspr0d012"})
}

func TestSharedCompletersCompletesRunJobShortIDs(t *testing.T) {
	ctx := completerContext(&fakeCloud{
		listJobs: func(page, limit int) (api.RunJobList, error) {
			items := []api.RunJob{{ShortID: "vgjggsynrr", Name: "train"}, {ShortID: "kq2mzx0bhd", Name: "eval"}}
			return api.RunJobList{Items: items}, nil
		},
	})

	got, err := Jobs("vg", ctx)
	wantCandidates(t, "vg", got, err, []string{"vgjggsynrr"})
}

func TestSharedCompletersCompletesVolumeNamesAndMountsAsNameColonUntilTheColonIsTyped(t *testing.T) {
	ctx := completerContext(&fakeCloud{
		listVolumes: func() ([]api.NetworkVolume, error) {
			return []api.NetworkVolume{datasetsVolume, myDataVolume}, nil
		},
	})

	got, err := Volumes("da", ctx)
	wantCandidates(t, "volumes da", got, err, []string{"datasets"})

	got, err = VolumeMounts("", ctx)
	wantCandidates(t, "mounts", got, err, []string{"datasets:", "my data:"})

	got, err = VolumeMounts("datasets:/w", ctx)
	wantCandidates(t, "mount path", got, err, []string{})
}

func TestSharedCompletersCompletesWhatToTypeForEachEnabledTemplateAndRegistryCredentialNames(t *testing.T) {
	ctx := completerContext(&fakeCloud{
		templates: func() ([]api.Template, error) {
			return []api.Template{
				{Slug: "rocm-pytorch", Visibility: api.VisibilityPublic, Official: true, Enabled: true, ShortID: "b2c3d4f5g6"},
				{Slug: "retired", Visibility: api.VisibilityPublic, Official: true, Enabled: false, ShortID: "h7j8k9m0n1"},
				{Slug: "trainer", Visibility: api.VisibilityPrivate, Official: false, Enabled: true, ShortID: "p2q3r4s5t6"},
				{Slug: "trainer", Visibility: api.VisibilityOrganization, Official: false, Enabled: true, ShortID: "v7w8x9y0z1"},
			}, nil
		},
		registryCredentials: func() ([]api.RegistryCredential, error) {
			return []api.RegistryCredential{{Name: "ghcr"}, {Name: "docker-hub"}}, nil
		},
	})

	got, err := Templates("", ctx)
	wantCandidates(t, "templates", got, err, []string{"rocm-pytorch", "trainer", "org/trainer"})

	got, err = TemplateIDs("p", ctx)
	wantCandidates(t, "template ids", got, err, []string{"p2q3r4s5t6"})

	got, err = Registries("g", ctx)
	wantCandidates(t, "registries", got, err, []string{"ghcr"})
}

func TestSharedCompletersCompletesTheGPUModelsGPUCountsAndCPUSizesOnOfferForTheWorkload(t *testing.T) {
	gpu := func(gpuModel string, gpuCount int) api.PriceRate {
		return api.PriceRate{
			ResourceType: api.ResourceTypeRunJobGPU,
			Unit:         api.UnitGPUHour,
			GPUCount:     new(gpuCount),
			Resources:    &api.Resources{GPUModel: new(gpuModel), CPUCores: 32},
		}
	}

	cpu := func(cores int) api.PriceRate {
		return api.PriceRate{
			ResourceType: api.ResourceTypeRunJobCPU,
			Unit:         api.UnitVCPUHour,
			GPUCount:     nil,
			Resources:    &api.Resources{GPUModel: nil, CPUCores: cores},
		}
	}

	var lastWorkload api.Workload

	ctx := completerContext(&fakeCloud{
		pricing: func(workload api.Workload) ([]api.PriceRate, error) {
			lastWorkload = workload
			storage := api.PriceRate{ResourceType: api.ResourceTypeStorage, Unit: api.UnitGiBMonth, GPUCount: nil}

			return []api.PriceRate{gpu("MI325X", 8), gpu("MI325X", 1), gpu("H100", 2), cpu(16), cpu(4), storage}, nil
		},
	})

	got, err := GPUTypes(api.WorkloadRunJob)("", ctx)
	wantCandidates(t, "gpu types", got, err, []string{"mi325x", "h100"})

	if lastWorkload != api.WorkloadRunJob {
		t.Errorf("gpu types priced %q, want run-job", lastWorkload)
	}

	got, err = GPUCounts(api.WorkloadInstance)("", ctx)
	wantCandidates(t, "gpu counts", got, err, []string{"1", "2", "8"})

	if lastWorkload != api.WorkloadInstance {
		t.Errorf("gpu counts priced %q, want instance", lastWorkload)
	}

	got, err = CPUCores(api.WorkloadInstance)("1", ctx)
	wantCandidates(t, "cpu cores", got, err, []string{"16"})
}

func TestSharedCompletersCompletesConfiguredProfileNames(t *testing.T) {
	swap(t, &LoadConfig, func() (*config.Config, error) {
		profiles := config.Profiles{{Name: "prod"}, {Name: "local"}}
		return &config.Config{Version: 1, Profiles: profiles}, nil
	})

	got, err := Profiles("l", completerContext(&fakeCloud{}))
	wantCandidates(t, "profiles", got, err, []string{"local"})
}
