/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CapacityAdvisorSpec = Target Utilization (os sliders do mockup).
// O operator compara o uso atual (requests / allocatable) com esses targets
// e calcula a capacidade adicional necessária.
type CapacityAdvisorSpec struct {
	// CPUTargetPercent is the desired max CPU request utilization (default 70).
	// Example: 70 means "try to keep requested CPU at or below 70% of allocatable".
	// +kubebuilder:default=70
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100
	// +optional
	CPUTargetPercent int32 `json:"cpuTargetPercent,omitempty"`

	// MemoryTargetPercent is the desired max memory request utilization (default 70).
	// +kubebuilder:default=70
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100
	// +optional
	MemoryTargetPercent int32 `json:"memoryTargetPercent,omitempty"`

	// PodsTargetPercent is the desired max pod density (default 80).
	// +kubebuilder:default=80
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=100
	// +optional
	PodsTargetPercent int32 `json:"podsTargetPercent,omitempty"`
}

// CapacityAdvisorStatus is what the Overview dashboard would read.
type CapacityAdvisorStatus struct {
	// Cluster-wide totals (top cards in the mockup).
	// +optional
	Cluster ClusterCapacity `json:"cluster,omitempty"`

	// Per MachineConfigPool breakdown (worker / infra / master table).
	// +optional
	Pools []PoolCapacity `json:"pools,omitempty"`

	// Recommendations derived from Spec targets.
	// +optional
	Recommendations Recommendations `json:"recommendations,omitempty"`

	// ObservedAt is when this status was last computed ("Last updated: 30s ago").
	// +optional
	ObservedAt metav1.Time `json:"observedAt,omitempty"`

	// Standard Kubernetes conditions (Ready, etc.).
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// ClusterCapacity mirrors the four summary cards.
type ClusterCapacity struct {
	// TotalCPUCores is Node allocatable CPU summed across the cluster (e.g. 576).
	TotalCPUCores float64 `json:"totalCPUCores"`
	// RequestedCPUCores is the sum of Pod CPU requests (e.g. 438).
	RequestedCPUCores float64 `json:"requestedCPUCores"`
	// CPUUsagePercent = requested / allocatable * 100.
	CPUUsagePercent float64 `json:"cpuUsagePercent"`

	// TotalMemoryBytes is Node allocatable memory (raw bytes).
	TotalMemoryBytes int64 `json:"totalMemoryBytes"`
	// RequestedMemoryBytes is the sum of Pod memory requests.
	RequestedMemoryBytes int64 `json:"requestedMemoryBytes"`
	// MemoryUsagePercent = requested / allocatable * 100.
	MemoryUsagePercent float64 `json:"memoryUsagePercent"`

	// PodCapacity is Node allocatable pods summed (e.g. 4500).
	PodCapacity int64 `json:"podCapacity"`
	// RunningPods counts non-terminal pods (Running + Pending).
	RunningPods int64 `json:"runningPods"`
	// PodsUsagePercent = running / capacity * 100.
	PodsUsagePercent float64 `json:"podsUsagePercent"`

	NodesTotal    int32 `json:"nodesTotal"`
	NodesReady    int32 `json:"nodesReady"`
	NodesNotReady int32 `json:"nodesNotReady"`
}

// PoolCapacity is one row in the "Capacity by MachineConfigPool" table.
type PoolCapacity struct {
	// Name of the MachineConfigPool (worker, infra, master, ...).
	Name string `json:"name"`
	// Nodes in this pool.
	Nodes int32 `json:"nodes"`

	TotalCPUCores     float64 `json:"totalCPUCores"`
	RequestedCPUCores float64 `json:"requestedCPUCores"`
	CPUUsagePercent   float64 `json:"cpuUsagePercent"`

	TotalMemoryBytes     int64   `json:"totalMemoryBytes"`
	RequestedMemoryBytes int64   `json:"requestedMemoryBytes"`
	MemoryUsagePercent   float64 `json:"memoryUsagePercent"`

	PodCapacity      int64   `json:"podCapacity"`
	RunningPods      int64   `json:"runningPods"`
	PodsUsagePercent float64 `json:"podsUsagePercent"`

	// Recommendation is a short human message: "Add 2 nodes", "Sufficient", "Consider upgrade".
	Recommendation string `json:"recommendation"`
}

// Recommendations is the "Capacity Recommendation" box (+cores, +memory, +pods, ~N nodes).
type Recommendations struct {
	// AdditionalCPUCores needed to bring usage down to the CPU target (0 if healthy).
	AdditionalCPUCores float64 `json:"additionalCPUCores"`
	// AdditionalMemoryBytes needed for the memory target.
	AdditionalMemoryBytes int64 `json:"additionalMemoryBytes"`
	// AdditionalPods capacity needed for the pods target.
	AdditionalPods int64 `json:"additionalPods"`
	// EstimatedNodes is a rough "how many worker-sized nodes" equivalent.
	EstimatedNodes int32 `json:"estimatedNodes"`
	// Message is the banner text ("Additional capacity is recommended." / healthy).
	Message string `json:"message"`
	// Attention is true when any resource is above its target.
	Attention bool `json:"attention"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="CPU%",type=number,JSONPath=`.status.cluster.cpuUsagePercent`
// +kubebuilder:printcolumn:name="Mem%",type=number,JSONPath=`.status.cluster.memoryUsagePercent`
// +kubebuilder:printcolumn:name="Pods%",type=number,JSONPath=`.status.cluster.podsUsagePercent`
// +kubebuilder:printcolumn:name="Attention",type=boolean,JSONPath=`.status.recommendations.attention`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// CapacityAdvisor reports OpenShift cluster capacity vs request utilization.
type CapacityAdvisor struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   CapacityAdvisorSpec   `json:"spec,omitempty"`
	Status CapacityAdvisorStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// CapacityAdvisorList contains a list of CapacityAdvisor
type CapacityAdvisorList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CapacityAdvisor `json:"items"`
}

func init() {
	SchemeBuilder.Register(&CapacityAdvisor{}, &CapacityAdvisorList{})
}
