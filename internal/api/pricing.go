package api

import "strings"

type ResourceType string

const (
	ResourceTypeInstanceGPU ResourceType = "instance.gpu"
	ResourceTypeInstanceCPU ResourceType = "instance.cpu"
	ResourceTypeRunJobGPU   ResourceType = "run-job.gpu"
	ResourceTypeRunJobCPU   ResourceType = "run-job.cpu"
	ResourceTypeStorage     ResourceType = "storage"
	ResourceTypeBandwidth   ResourceType = "bandwidth"
)

var _ = registerEnum(
	ResourceTypeInstanceGPU, ResourceTypeInstanceCPU, ResourceTypeRunJobGPU, ResourceTypeRunJobCPU,
	ResourceTypeStorage, ResourceTypeBandwidth,
)

type PriceUnit string

const (
	UnitGPUHour  PriceUnit = "gpu_hour"
	UnitVCPUHour PriceUnit = "vcpu_hour"
	UnitGiBMonth PriceUnit = "gib_month"
	UnitGiB      PriceUnit = "gib"
)

var _ = registerEnum(UnitGPUHour, UnitVCPUHour, UnitGiBMonth, UnitGiB)

// PriceRate is a GET /instances/pricing (or /run-jobs/pricing) entry.
type PriceRate struct {
	SKU                      string       `json:"sku"`
	DisplayName              string       `json:"displayName"`
	ResourceType             ResourceType `json:"resourceType"`
	Unit                     PriceUnit    `json:"unit"`
	GPUCount                 *int         `json:"gpuCount"`
	UnitPriceMicros          string       `json:"unitPriceMicros"`
	CompareAtUnitPriceMicros *string      `json:"compareAtUnitPriceMicros"`
	Currency                 string       `json:"currency"`
	EffectiveAt              string       `json:"effectiveAt"`
	ExpiresAt                *string      `json:"expiresAt"`
	Resources                *Resources   `json:"resources,omitzero"`
}

// ComputeTypeOf is the kind of compute a rate sells, whichever workload it is
// sold to; empty for storage and bandwidth.
func ComputeTypeOf(rate PriceRate) ComputeType {
	resourceType := string(rate.ResourceType)

	if strings.HasSuffix(resourceType, ".gpu") {
		return ComputeGPU
	}

	if strings.HasSuffix(resourceType, ".cpu") {
		return ComputeCPU
	}

	return ""
}
