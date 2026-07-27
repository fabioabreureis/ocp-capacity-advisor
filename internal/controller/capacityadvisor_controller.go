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

package controller

import (
	"context"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	advisorv1alpha1 "github.com/openshift/ocp-capacity-advisor/api/v1alpha1"
	"github.com/openshift/ocp-capacity-advisor/internal/advisor"
	"github.com/openshift/ocp-capacity-advisor/internal/collector"
)

// RequeueInterval matches the mockup "Last updated: 30s ago".
const RequeueInterval = 30 * time.Second

// CapacityAdvisorReconciler reconciles a CapacityAdvisor object.
// Flow: collect → advise → update status. Nothing else.
type CapacityAdvisorReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=advisor.openshift.io,resources=capacityadvisors,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=advisor.openshift.io,resources=capacityadvisors/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=advisor.openshift.io,resources=capacityadvisors/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups=machineconfiguration.openshift.io,resources=machineconfigpools,verbs=get;list;watch

func (r *CapacityAdvisorReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// 1) Load the CR the user created (e.g. name: cluster).
	var ca advisorv1alpha1.CapacityAdvisor
	if err := r.Get(ctx, req.NamespacedName, &ca); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	targets := advisor.ApplyDefaults(advisor.Targets{
		CPUPercent:    ca.Spec.CPUTargetPercent,
		MemoryPercent: ca.Spec.MemoryTargetPercent,
		PodsPercent:   ca.Spec.PodsTargetPercent,
	})

	// 2) Collect capacity from the API.
	snap, err := collector.Collect(ctx, r.Client)
	if err != nil {
		logger.Error(err, "collect failed")
		return ctrl.Result{RequeueAfter: RequeueInterval}, err
	}

	// 3) Advise (pure math).
	clusterAdvice := advisor.AdviseCluster(advisor.ClusterInput{
		CPUAlloc:    collector.MilliToCores(snap.Cluster.CPUAllocMilli),
		CPUReq:      collector.MilliToCores(snap.Cluster.CPUReqMilli),
		MemAlloc:    float64(snap.Cluster.MemAllocBytes),
		MemReq:      float64(snap.Cluster.MemReqBytes),
		PodAlloc:    float64(snap.Cluster.PodAlloc),
		PodRunning:  float64(snap.Cluster.PodRunning),
		WorkerShape: advisor.NodeShape(snap.WorkerShape),
	}, targets)

	pools := make([]advisorv1alpha1.PoolCapacity, 0, len(snap.Pools))
	for _, p := range snap.Pools {
		pr := advisor.AdvisePool(advisor.PoolInput{
			Name:           p.Name,
			Nodes:          p.Nodes,
			CPUAlloc:       collector.MilliToCores(p.CPUAllocMilli),
			CPUReq:         collector.MilliToCores(p.CPUReqMilli),
			MemAlloc:       float64(p.MemAllocBytes),
			MemReq:         float64(p.MemReqBytes),
			PodAlloc:       float64(p.PodAlloc),
			PodRunning:     float64(p.PodRunning),
			IsControlPlane: p.IsControlPlane,
		}, targets)

		pools = append(pools, advisorv1alpha1.PoolCapacity{
			Name:                 p.Name,
			Nodes:                p.Nodes,
			TotalCPUCores:        collector.MilliToCores(p.CPUAllocMilli),
			RequestedCPUCores:    collector.MilliToCores(p.CPUReqMilli),
			CPUUsagePercent:      pr.CPUUsagePercent,
			TotalMemoryBytes:     p.MemAllocBytes,
			RequestedMemoryBytes: p.MemReqBytes,
			MemoryUsagePercent:   pr.MemoryUsagePercent,
			PodCapacity:          p.PodAlloc,
			RunningPods:          p.PodRunning,
			PodsUsagePercent:     pr.PodsUsagePercent,
			Recommendation:       pr.Recommendation,
		})
	}

	// 4) Write status.
	ca.Status.Cluster = advisorv1alpha1.ClusterCapacity{
		TotalCPUCores:        collector.MilliToCores(snap.Cluster.CPUAllocMilli),
		RequestedCPUCores:    collector.MilliToCores(snap.Cluster.CPUReqMilli),
		CPUUsagePercent:      clusterAdvice.CPUUsagePercent,
		TotalMemoryBytes:     snap.Cluster.MemAllocBytes,
		RequestedMemoryBytes: snap.Cluster.MemReqBytes,
		MemoryUsagePercent:   clusterAdvice.MemoryUsagePercent,
		PodCapacity:          snap.Cluster.PodAlloc,
		RunningPods:          snap.Cluster.PodRunning,
		PodsUsagePercent:     clusterAdvice.PodsUsagePercent,
		NodesTotal:           snap.Cluster.NodesTotal,
		NodesReady:           snap.Cluster.NodesReady,
		NodesNotReady:        snap.Cluster.NodesNotReady,
	}
	ca.Status.Pools = pools
	ca.Status.Recommendations = advisorv1alpha1.Recommendations{
		AdditionalCPUCores:    clusterAdvice.AdditionalCPUCores,
		AdditionalMemoryBytes: clusterAdvice.AdditionalMemoryBytes,
		AdditionalPods:        clusterAdvice.AdditionalPods,
		EstimatedNodes:        clusterAdvice.EstimatedNodes,
		Message:               clusterAdvice.Message,
		Attention:             clusterAdvice.Attention,
	}
	ca.Status.ObservedAt = metav1.Now()
	ca.Status.Conditions = []metav1.Condition{
		{
			Type:               "Ready",
			Status:             metav1.ConditionTrue,
			Reason:             "Collected",
			Message:            clusterAdvice.Message,
			LastTransitionTime: metav1.Now(),
		},
	}

	if err := r.Status().Update(ctx, &ca); err != nil {
		logger.Error(err, "status update failed")
		return ctrl.Result{}, err
	}

	logger.Info("capacity status updated",
		"cpuUsage%", clusterAdvice.CPUUsagePercent,
		"attention", clusterAdvice.Attention,
	)

	// 5) Requeue so numbers stay fresh even without CR edits.
	return ctrl.Result{RequeueAfter: RequeueInterval}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *CapacityAdvisorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&advisorv1alpha1.CapacityAdvisor{}).
		Complete(r)
}
