/*
Package collector talks to the Kubernetes API and returns plain numbers.

It does NOT compute recommendations — that lives in internal/advisor.
*/
package collector

import (
	"context"
	"fmt"
	"sort"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Snapshot is everything the advisor needs for one reconcile.
type Snapshot struct {
	Cluster ClusterTotals
	Pools   []PoolTotals
	// WorkerShape is the average worker node size (for "estimated N nodes").
	WorkerShape NodeShape
}

// ClusterTotals are cluster-wide sums.
type ClusterTotals struct {
	CPUAllocMilli int64
	CPUReqMilli   int64
	MemAllocBytes int64
	MemReqBytes   int64
	PodAlloc      int64
	PodRunning    int64
	NodesTotal    int32
	NodesReady    int32
	NodesNotReady int32
}

// PoolTotals are per MachineConfigPool (or role fallback) sums.
type PoolTotals struct {
	Name           string
	Nodes          int32
	CPUAllocMilli  int64
	CPUReqMilli    int64
	MemAllocBytes  int64
	MemReqBytes    int64
	PodAlloc       int64
	PodRunning     int64
	IsControlPlane bool
}

// NodeShape is average capacity of one node.
type NodeShape struct {
	CPUCores    float64
	MemoryBytes float64
	PodCapacity float64
}

var mcpGVK = schema.GroupVersionKind{
	Group:   "machineconfiguration.openshift.io",
	Version: "v1",
	Kind:    "MachineConfigPool",
}

// Collect lists Nodes + Pods (+ MachineConfigPools when available) and aggregates.
func Collect(ctx context.Context, c client.Client) (Snapshot, error) {
	var nodes corev1.NodeList
	if err := c.List(ctx, &nodes); err != nil {
		return Snapshot{}, fmt.Errorf("list nodes: %w", err)
	}

	var pods corev1.PodList
	if err := c.List(ctx, &pods); err != nil {
		return Snapshot{}, fmt.Errorf("list pods: %w", err)
	}

	// Per-node request / running-pod counters.
	type nodeAgg struct {
		cpuReqMilli int64
		memReqBytes int64
		runningPods int64
	}
	byNode := map[string]*nodeAgg{}
	for i := range nodes.Items {
		byNode[nodes.Items[i].Name] = &nodeAgg{}
	}

	for i := range pods.Items {
		p := &pods.Items[i]
		// Ignore finished pods — they no longer reserve scheduling capacity.
		if p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed {
			continue
		}
		if p.Spec.NodeName == "" {
			// Unscheduled Pending still counts toward "occupancy" pressure at cluster level
			// but cannot be attributed to a node/pool yet — skip per-node, count later if needed.
			continue
		}
		agg, ok := byNode[p.Spec.NodeName]
		if !ok {
			continue
		}
		agg.runningPods++
		agg.cpuReqMilli += podCPURequestMilli(p)
		agg.memReqBytes += podMemoryRequestBytes(p)
	}

	// Map node → pool name via MachineConfigPool.nodeSelector (OpenShift).
	nodePool, poolIsCP, err := mapNodesToPools(ctx, c, nodes.Items)
	if err != nil {
		// Not on OpenShift or no MCP access — fall back to node-role labels.
		nodePool, poolIsCP = mapNodesToRoles(nodes.Items)
	}

	pools := map[string]*PoolTotals{}
	var cluster ClusterTotals

	for i := range nodes.Items {
		n := &nodes.Items[i]
		ready := isNodeReady(n)
		cluster.NodesTotal++
		if ready {
			cluster.NodesReady++
		} else {
			cluster.NodesNotReady++
		}

		cpuAlloc := n.Status.Allocatable.Cpu().MilliValue()
		memAlloc := n.Status.Allocatable.Memory().Value()
		podAlloc := n.Status.Allocatable.Pods().Value()

		agg := byNode[n.Name]
		var cpuReq, memReq, running int64
		if agg != nil {
			cpuReq, memReq, running = agg.cpuReqMilli, agg.memReqBytes, agg.runningPods
		}

		cluster.CPUAllocMilli += cpuAlloc
		cluster.MemAllocBytes += memAlloc
		cluster.PodAlloc += podAlloc
		cluster.CPUReqMilli += cpuReq
		cluster.MemReqBytes += memReq
		cluster.PodRunning += running

		poolName := nodePool[n.Name]
		if poolName == "" {
			poolName = "unknown"
		}
		pt, ok := pools[poolName]
		if !ok {
			pt = &PoolTotals{Name: poolName, IsControlPlane: poolIsCP[poolName]}
			pools[poolName] = pt
		}
		pt.Nodes++
		pt.CPUAllocMilli += cpuAlloc
		pt.MemAllocBytes += memAlloc
		pt.PodAlloc += podAlloc
		pt.CPUReqMilli += cpuReq
		pt.MemReqBytes += memReq
		pt.PodRunning += running
	}

	snap := Snapshot{Cluster: cluster}
	for _, p := range pools {
		snap.Pools = append(snap.Pools, *p)
	}
	sort.Slice(snap.Pools, func(i, j int) bool {
		return snap.Pools[i].Name < snap.Pools[j].Name
	})

	// Average worker node shape for cluster-level "estimated nodes".
	if w, ok := pools["worker"]; ok && w.Nodes > 0 {
		snap.WorkerShape = NodeShape{
			CPUCores:    float64(w.CPUAllocMilli) / 1000.0 / float64(w.Nodes),
			MemoryBytes: float64(w.MemAllocBytes) / float64(w.Nodes),
			PodCapacity: float64(w.PodAlloc) / float64(w.Nodes),
		}
	}

	return snap, nil
}

// mapNodesToPools uses MachineConfigPool.spec.nodeSelector.matchLabels.
func mapNodesToPools(ctx context.Context, c client.Client, nodes []corev1.Node) (map[string]string, map[string]bool, error) {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(schema.GroupVersionKind{
		Group:   mcpGVK.Group,
		Version: mcpGVK.Version,
		Kind:    mcpGVK.Kind + "List",
	})
	if err := c.List(ctx, list); err != nil {
		return nil, nil, err
	}
	if len(list.Items) == 0 {
		return nil, nil, fmt.Errorf("no MachineConfigPools found")
	}

	nodePool := map[string]string{}
	poolIsCP := map[string]bool{}

	for _, item := range list.Items {
		name := item.GetName()
		poolIsCP[name] = name == "master" || name == "control-plane"

		matchLabels, found, _ := unstructured.NestedStringMap(item.Object, "spec", "nodeSelector", "matchLabels")
		if !found || len(matchLabels) == 0 {
			// Some MCPs only set machineConfigSelector; fall back to name-as-role label.
			matchLabels = map[string]string{"node-role.kubernetes.io/" + name: ""}
		}

		for i := range nodes {
			if labelsMatch(nodes[i].Labels, matchLabels) {
				nodePool[nodes[i].Name] = name
			}
		}
	}
	return nodePool, poolIsCP, nil
}

// mapNodesToRoles is used when MachineConfigPool is unavailable (e.g. plain Kubernetes).
func mapNodesToRoles(nodes []corev1.Node) (map[string]string, map[string]bool) {
	nodePool := map[string]string{}
	poolIsCP := map[string]bool{"master": true, "control-plane": true}
	for i := range nodes {
		n := &nodes[i]
		switch {
		case hasRole(n.Labels, "master") || hasRole(n.Labels, "control-plane"):
			nodePool[n.Name] = "master"
		case hasRole(n.Labels, "infra"):
			nodePool[n.Name] = "infra"
		case hasRole(n.Labels, "worker"):
			nodePool[n.Name] = "worker"
		default:
			nodePool[n.Name] = "worker"
		}
	}
	return nodePool, poolIsCP
}

func hasRole(labels map[string]string, role string) bool {
	_, ok := labels["node-role.kubernetes.io/"+role]
	return ok
}

// labelsMatch checks that every key in want exists on have.
// Empty value in want means "label key present" (OpenShift role labels often have empty values).
func labelsMatch(have, want map[string]string) bool {
	for k, v := range want {
		hv, ok := have[k]
		if !ok {
			return false
		}
		if v != "" && hv != v {
			return false
		}
	}
	return true
}

func isNodeReady(n *corev1.Node) bool {
	for _, c := range n.Status.Conditions {
		if c.Type == corev1.NodeReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

func podCPURequestMilli(p *corev1.Pod) int64 {
	var total int64
	for _, c := range p.Spec.Containers {
		total += c.Resources.Requests.Cpu().MilliValue()
	}
	// Init containers: Kubernetes uses max(init), not sum — keep it simple for learning:
	// we take the max init request (closer to scheduler behavior).
	var maxInit int64
	for _, c := range p.Spec.InitContainers {
		if m := c.Resources.Requests.Cpu().MilliValue(); m > maxInit {
			maxInit = m
		}
	}
	if maxInit > total {
		return maxInit
	}
	return total
}

func podMemoryRequestBytes(p *corev1.Pod) int64 {
	var total int64
	for _, c := range p.Spec.Containers {
		total += c.Resources.Requests.Memory().Value()
	}
	var maxInit int64
	for _, c := range p.Spec.InitContainers {
		if m := c.Resources.Requests.Memory().Value(); m > maxInit {
			maxInit = m
		}
	}
	if maxInit > total {
		return maxInit
	}
	return total
}

// MilliToCores converts millicores to cores (1000m → 1.0).
func MilliToCores(milli int64) float64 {
	return float64(milli) / 1000.0
}
