/*
Package advisor only does math — no Kubernetes client.

Formula (same for CPU, memory, pods):

	usage = requested / allocatable
	if usage > target:
	    additional = (requested / target) - allocatable
	else:
	    additional = 0

target is a fraction, e.g. 70% → 0.70.
*/
package advisor

import (
	"fmt"
	"math"
)

// Targets comes from CapacityAdvisorSpec (percentages 1–100).
type Targets struct {
	CPUPercent    int32
	MemoryPercent int32
	PodsPercent   int32
}

// Defaults matching the mockup sliders.
func DefaultTargets() Targets {
	return Targets{CPUPercent: 70, MemoryPercent: 70, PodsPercent: 80}
}

// ApplyDefaults fills zero values with mockup defaults.
func ApplyDefaults(t Targets) Targets {
	d := DefaultTargets()
	if t.CPUPercent <= 0 {
		t.CPUPercent = d.CPUPercent
	}
	if t.MemoryPercent <= 0 {
		t.MemoryPercent = d.MemoryPercent
	}
	if t.PodsPercent <= 0 {
		t.PodsPercent = d.PodsPercent
	}
	return t
}

// ResourceSnap is allocatable vs requested for one resource dimension.
type ResourceSnap struct {
	Allocatable float64
	Requested   float64
}

// UsagePercent returns requested/allocatable*100 (0 if no capacity).
func UsagePercent(r ResourceSnap) float64 {
	if r.Allocatable <= 0 {
		return 0
	}
	return (r.Requested / r.Allocatable) * 100
}

// Additional returns how much more allocatable capacity is needed so that
// requested/allocatable <= targetPercent/100.
//
// Example: requested=438, allocatable=576, target=70
//
//	438/576 ≈ 76% > 70% → additional = 438/0.70 - 576 ≈ 49.7
func Additional(requested, allocatable float64, targetPercent int32) float64 {
	if allocatable <= 0 || targetPercent <= 0 {
		return 0
	}
	target := float64(targetPercent) / 100.0
	usage := requested / allocatable
	if usage <= target {
		return 0
	}
	needed := requested / target
	return needed - allocatable
}

// NodeShape is the average size of a worker node (for "estimated N nodes").
type NodeShape struct {
	CPUCores    float64
	MemoryBytes float64
	PodCapacity float64
}

// EstimateNodes picks the max of CPU/mem/pods pressure converted to node count.
func EstimateNodes(extraCPU, extraMem, extraPods float64, shape NodeShape) int32 {
	if shape.CPUCores <= 0 && shape.MemoryBytes <= 0 && shape.PodCapacity <= 0 {
		return 0
	}
	var n float64
	if shape.CPUCores > 0 && extraCPU > 0 {
		n = math.Max(n, extraCPU/shape.CPUCores)
	}
	if shape.MemoryBytes > 0 && extraMem > 0 {
		n = math.Max(n, extraMem/shape.MemoryBytes)
	}
	if shape.PodCapacity > 0 && extraPods > 0 {
		n = math.Max(n, extraPods/shape.PodCapacity)
	}
	if n <= 0 {
		return 0
	}
	return int32(math.Ceil(n))
}

// PoolInput is raw numbers for one MachineConfigPool (or role group).
type PoolInput struct {
	Name           string
	Nodes          int32
	CPUAlloc       float64 // cores
	CPUReq         float64
	MemAlloc       float64 // bytes
	MemReq         float64
	PodAlloc       float64
	PodRunning     float64
	IsControlPlane bool // master/control-plane → "Consider upgrade"
}

// PoolResult is the advice for one pool.
type PoolResult struct {
	Name                  string
	Nodes                 int32
	CPUUsagePercent       float64
	MemoryUsagePercent    float64
	PodsUsagePercent      float64
	AdditionalCPUCores    float64
	AdditionalMemoryBytes float64
	AdditionalPods        float64
	Recommendation        string
}

// AdvisePool computes usage + short recommendation text for one pool.
func AdvisePool(p PoolInput, t Targets) PoolResult {
	t = ApplyDefaults(t)
	cpuAdd := Additional(p.CPUReq, p.CPUAlloc, t.CPUPercent)
	memAdd := Additional(p.MemReq, p.MemAlloc, t.MemoryPercent)
	podAdd := Additional(p.PodRunning, p.PodAlloc, t.PodsPercent)

	out := PoolResult{
		Name:                  p.Name,
		Nodes:                 p.Nodes,
		CPUUsagePercent:       UsagePercent(ResourceSnap{p.CPUAlloc, p.CPUReq}),
		MemoryUsagePercent:    UsagePercent(ResourceSnap{p.MemAlloc, p.MemReq}),
		PodsUsagePercent:      UsagePercent(ResourceSnap{p.PodAlloc, p.PodRunning}),
		AdditionalCPUCores:    cpuAdd,
		AdditionalMemoryBytes: memAdd,
		AdditionalPods:        podAdd,
	}

	switch {
	case p.IsControlPlane && (cpuAdd > 0 || memAdd > 0 || podAdd > 0):
		out.Recommendation = "Consider upgrade"
	case cpuAdd <= 0 && memAdd <= 0 && podAdd <= 0:
		out.Recommendation = "Sufficient"
	default:
		// Estimate nodes from this pool's average node size.
		shape := NodeShape{}
		if p.Nodes > 0 {
			shape.CPUCores = p.CPUAlloc / float64(p.Nodes)
			shape.MemoryBytes = p.MemAlloc / float64(p.Nodes)
			shape.PodCapacity = p.PodAlloc / float64(p.Nodes)
		}
		n := EstimateNodes(cpuAdd, memAdd, podAdd, shape)
		if n > 0 {
			out.Recommendation = fmt.Sprintf("Add %d nodes", n)
		} else {
			out.Recommendation = "Additional capacity needed"
		}
	}
	return out
}

// ClusterInput is cluster-wide totals + optional worker shape for estimates.
type ClusterInput struct {
	CPUAlloc    float64
	CPUReq      float64
	MemAlloc    float64
	MemReq      float64
	PodAlloc    float64
	PodRunning  float64
	WorkerShape NodeShape
}

// ClusterAdvice is the recommendation box.
type ClusterAdvice struct {
	AdditionalCPUCores    float64
	AdditionalMemoryBytes int64
	AdditionalPods        int64
	EstimatedNodes        int32
	Attention             bool
	Message               string
	CPUUsagePercent       float64
	MemoryUsagePercent    float64
	PodsUsagePercent      float64
}

// AdviseCluster computes cluster-level recommendations.
func AdviseCluster(c ClusterInput, t Targets) ClusterAdvice {
	t = ApplyDefaults(t)
	cpuAdd := Additional(c.CPUReq, c.CPUAlloc, t.CPUPercent)
	memAdd := Additional(c.MemReq, c.MemAlloc, t.MemoryPercent)
	podAdd := Additional(c.PodRunning, c.PodAlloc, t.PodsPercent)

	out := ClusterAdvice{
		AdditionalCPUCores:    round1(cpuAdd),
		AdditionalMemoryBytes: int64(math.Ceil(memAdd)),
		AdditionalPods:        int64(math.Ceil(podAdd)),
		EstimatedNodes:        EstimateNodes(cpuAdd, memAdd, podAdd, c.WorkerShape),
		CPUUsagePercent:       round1(UsagePercent(ResourceSnap{c.CPUAlloc, c.CPUReq})),
		MemoryUsagePercent:    round1(UsagePercent(ResourceSnap{c.MemAlloc, c.MemReq})),
		PodsUsagePercent:      round1(UsagePercent(ResourceSnap{c.PodAlloc, c.PodRunning})),
	}
	out.Attention = out.AdditionalCPUCores > 0 || out.AdditionalMemoryBytes > 0 || out.AdditionalPods > 0
	if out.Attention {
		out.Message = "Additional capacity is recommended. Based on your target utilization thresholds, the cluster requires additional capacity."
	} else {
		out.Message = "Cluster capacity is within target utilization thresholds."
	}
	return out
}

func round1(v float64) float64 {
	return math.Round(v*10) / 10
}
