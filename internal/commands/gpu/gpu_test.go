package gpu

import (
	"reflect"
	"testing"

	"featherless.ai/featherless-cloud/internal/api"
)

func gpuRate(sku string, count int, model string, memory int, micros string) api.PriceRate {
	return api.PriceRate{
		SKU:             sku,
		DisplayName:     model,
		ResourceType:    api.ResourceTypeRunJobGPU,
		Unit:            api.UnitGPUHour,
		GPUCount:        &count,
		UnitPriceMicros: micros,
		Resources:       &api.Resources{GPUModel: &model, GPUMemoryGiB: &memory},
	}
}

func TestCollectMergesBothWorkloadsIntoOneModel(t *testing.T) {
	instance := []api.PriceRate{
		gpuRate("instance.gpu.mi325x.1", 1, "MI325X", 256, "3290000"),
		gpuRate("instance.gpu.mi325x.4", 4, "MI325X", 256, "3090000"),
	}
	runJob := []api.PriceRate{
		gpuRate("run-job.gpu.mi325x.1", 1, "MI325X", 256, "2590000"),
		gpuRate("run-job.gpu.mi325x.2", 2, "MI325X", 256, "2490000"),
		{SKU: "storage.network", DisplayName: "Network storage", ResourceType: api.ResourceTypeStorage, Unit: api.UnitGiBMonth},
	}

	got := collect(instance, runJob)

	if len(got) != 1 {
		t.Fatalf("got %d models, want one: %+v", len(got), got)
	}

	model := got[0]

	if model.Model != "MI325X" {
		t.Errorf("model = %q", model.Model)
	}

	if want := []int{1, 2, 4}; !reflect.DeepEqual(model.GPUCounts, want) {
		t.Errorf("counts = %v, want %v", model.GPUCounts, want)
	}

	if model.MemoryGiB == nil || *model.MemoryGiB != 256 {
		t.Errorf("memory = %v", model.MemoryGiB)
	}

	if model.InstanceSKU != "instance.gpu.mi325x.1" || model.InstancePriceMicros != "3290000" {
		t.Errorf("instance = %q %q, want the single-GPU rate", model.InstanceSKU, model.InstancePriceMicros)
	}

	if model.RunJobSKU != "run-job.gpu.mi325x.1" || model.RunJobPriceMicros != "2590000" {
		t.Errorf("run job = %q %q, want the single-GPU rate", model.RunJobSKU, model.RunJobPriceMicros)
	}
}

func TestCollectSortsModelsAndSkipsCPURates(t *testing.T) {
	rates := []api.PriceRate{
		gpuRate("a.gpu.zeta.1", 1, "Zeta", 80, "1000000"),
		gpuRate("a.gpu.alpha.1", 1, "Alpha", 40, "2000000"),
		{SKU: "run-job.cpu.4", ResourceType: api.ResourceTypeRunJobCPU, Unit: api.UnitVCPUHour},
	}

	got := collect(nil, rates)

	if len(got) != 2 || got[0].Model != "Alpha" || got[1].Model != "Zeta" {
		t.Errorf("got %+v, want Alpha then Zeta", got)
	}
}
