package advisor

import (
	"math"
	"testing"
)

func almostEqual(a, b, eps float64) bool {
	return math.Abs(a-b) <= eps
}

func TestAdditional_Healthy(t *testing.T) {
	// Clearly under 70%: 50% usage → no extra capacity.
	got := Additional(1.0, 2.0, 70)
	if got != 0 {
		t.Fatalf("expected 0 additional, got %v", got)
	}
}

func TestAdditional_CPUAboveTarget(t *testing.T) {
	// Mockup-style numbers: 438 requested / 576 allocatable ≈ 76% > 70%.
	got := Additional(438, 576, 70)
	want := 438.0/0.70 - 576 // ≈ 49.71
	if !almostEqual(got, want, 0.01) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestAdditional_PodsAboveTarget(t *testing.T) {
	// 3920 running / 4500 capacity ≈ 87% > 80%.
	got := Additional(3920, 4500, 80)
	want := 3920.0/0.80 - 4500 // = 400
	if !almostEqual(got, want, 0.01) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestUsagePercent(t *testing.T) {
	got := UsagePercent(ResourceSnap{Allocatable: 576, Requested: 438})
	if !almostEqual(got, 76.0416, 0.01) {
		t.Fatalf("got %v", got)
	}
}

func TestEstimateNodes(t *testing.T) {
	// Worker shape from mockup: 32 cores / 128Gi / 250 pods.
	shape := NodeShape{CPUCores: 32, MemoryBytes: 128 << 30, PodCapacity: 250}
	n := EstimateNodes(187, 650*(1<<30)/1024 /* rough */, 600, shape)
	if n < 1 {
		t.Fatalf("expected at least 1 node, got %d", n)
	}
	// Pods alone: ceil(600/250) = 3; CPU: ceil(187/32) = 6 → max = 6
	n2 := EstimateNodes(187, 0, 600, shape)
	if n2 != 6 {
		t.Fatalf("expected 6 nodes, got %d", n2)
	}
}

func TestAdvisePool_MasterUpgrade(t *testing.T) {
	p := PoolInput{
		Name: "master", Nodes: 3,
		CPUAlloc: 96, CPUReq: 74,
		MemAlloc: 384 << 30, MemReq: 360 << 30,
		PodAlloc: 750, PodRunning: 650,
		IsControlPlane: true,
	}
	r := AdvisePool(p, DefaultTargets())
	if r.Recommendation != "Consider upgrade" {
		t.Fatalf("got %q", r.Recommendation)
	}
}

func TestAdvisePool_Sufficient(t *testing.T) {
	p := PoolInput{
		Name: "infra", Nodes: 3,
		CPUAlloc: 96, CPUReq: 50,
		MemAlloc: 384 << 30, MemReq: 200 << 30,
		PodAlloc: 750, PodRunning: 400,
	}
	r := AdvisePool(p, DefaultTargets())
	if r.Recommendation != "Sufficient" {
		t.Fatalf("got %q", r.Recommendation)
	}
}

func TestAdviseCluster_Attention(t *testing.T) {
	c := ClusterInput{
		CPUAlloc: 576, CPUReq: 438,
		MemAlloc: 2.30 * (1 << 40), MemReq: 1.67 * (1 << 40), // TiB-ish
		PodAlloc: 4500, PodRunning: 3920,
		WorkerShape: NodeShape{CPUCores: 32, MemoryBytes: 128 << 30, PodCapacity: 250},
	}
	a := AdviseCluster(c, DefaultTargets())
	if !a.Attention {
		t.Fatal("expected attention")
	}
	if a.AdditionalCPUCores <= 0 {
		t.Fatalf("expected additional CPU, got %v", a.AdditionalCPUCores)
	}
	if a.AdditionalPods <= 0 {
		t.Fatalf("expected additional pods, got %v", a.AdditionalPods)
	}
}
