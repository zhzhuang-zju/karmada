/*
Copyright 2021 The Karmada Authors.

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

package core

import (
	"fmt"
	"time"

	"k8s.io/klog/v2"

	policyv1alpha1 "github.com/karmada-io/karmada/pkg/apis/policy/v1alpha1"
	workv1alpha2 "github.com/karmada-io/karmada/pkg/apis/work/v1alpha2"
	"github.com/karmada-io/karmada/pkg/scheduler/core/spreadconstraint"
	"github.com/karmada-io/karmada/pkg/scheduler/framework"
	"github.com/karmada-io/karmada/pkg/scheduler/metrics"
)

// SelectClusters selects clusters based on the placement and resource binding spec.
func SelectClusters(clustersScore framework.ClusterScoreList, placement *policyv1alpha1.Placement, spec *workv1alpha2.ResourceBindingSpec) ([]spreadconstraint.ClusterDetailInfo, error) {
	startTime := time.Now()
	defer metrics.ScheduleStep(metrics.ScheduleStepSelect, startTime)

	groupClustersInfo := spreadconstraint.GroupClustersWithScore(clustersScore, placement, spec, calAvailableReplicas)
	return spreadconstraint.SelectBestClusters(placement, groupClustersInfo, spec.Replicas)
}

// AssignReplicas assigns replicas to clusters based on the placement and resource binding spec.
func AssignReplicas(clusters []spreadconstraint.ClusterDetailInfo, spec *workv1alpha2.ResourceBindingSpec, status *workv1alpha2.ResourceBindingStatus) ([]workv1alpha2.TargetCluster, error) {
	startTime := time.Now()
	defer metrics.ScheduleStep(metrics.ScheduleStepAssignReplicas, startTime)

	if len(clusters) == 0 {
		return nil, fmt.Errorf("no clusters available to schedule")
	}

	// Only workloads should participate in replica assignments.
	// Workloads with multiple components should be excluded from replica assignment because:
	//   1) They don't support replica division.
	//   2) They usually don't implement the ReviseReplica interpreter; even if replicas are assigned, the result cannot be applied by the controller.
	// For single pod template workloads (like Deployment), if the replica is 0 and no resource requirements are specified,
	// assignment is bypassed, causing the workload to be propagated to all candidate clusters. This is a known issue, and
	// a suggested fix is: when parsing replicas and requirements, set them to components. This should be addressed along with
	// the deprecation of binding.spec.replicas and binding.spec.ReplicaRequirements. After that, the check should be changed to
	// len(spec.Components) == 1.
	if (spec.Replicas > 0 || spec.ReplicaRequirements != nil) && len(spec.Components) <= 1 {
		if spec.Placement.ClusterAffinity == nil && spec.Placement.ClusterAffinities != nil && spec.Placement.AffinityStrategy.Mode == "Cascade" {
			return assignReplicasCascade(clusters, spec, status)
		}
		return assignReplicas(clusters, spec, status)
	}

	// For non-workloads (e.g., Service, Config) and multi-component workloads (e.g., FlinkDeployment), propagate to all candidate clusters.
	targetClusters := make([]workv1alpha2.TargetCluster, len(clusters))
	for i, cluster := range clusters {
		targetClusters[i] = workv1alpha2.TargetCluster{Name: cluster.Cluster.Name}
	}
	return targetClusters, nil
}

func assignReplicasCascade(clusters []spreadconstraint.ClusterDetailInfo, spec *workv1alpha2.ResourceBindingSpec, status *workv1alpha2.ResourceBindingStatus) ([]workv1alpha2.TargetCluster, error) {
	clusterTiers := make(map[int64][]spreadconstraint.ClusterDetailInfo)
	for i, cluster := range clusters {
		tier := cluster.Score / framework.TierBaseScore
		clusterTiers[tier] = append(clusterTiers[tier], clusters[i])
	}

	var finalResults []workv1alpha2.TargetCluster
	remaining := spec.Replicas
	for i := len(clusterTiers); i > 0; i-- {
		sameTierClusters := clusterTiers[int64(i)]
		toBeAssigned := min(int64(remaining), sumAvailableReplicas(sameTierClusters))
		spec.Replicas = int32(toBeAssigned)
		result, err := assignReplicas(sameTierClusters, spec, status)
		if err != nil {
			return nil, err
		}
		klog.Infof("1111111111111111 %+v", sameTierClusters)
		klog.Infof("2222222222222222 %+v", result)

		finalResults = append(finalResults, result...)
		remaining -= int32(toBeAssigned)

		if remaining <= 0 {
			break
		}
	}

	if remaining > 0 {
		return nil, &framework.UnschedulableError{Message: "Clusters available replicas are not enough to schedule."}
	}
	return finalResults, nil
}

func sumAvailableReplicas(clusters []spreadconstraint.ClusterDetailInfo) int64 {
	var total int64
	for _, cluster := range clusters {
		total += cluster.AvailableReplicas
	}
	return total
}

func assignReplicas(clusters []spreadconstraint.ClusterDetailInfo, spec *workv1alpha2.ResourceBindingSpec, status *workv1alpha2.ResourceBindingStatus) ([]workv1alpha2.TargetCluster, error) {
	state := newAssignState(clusters, spec, status)
	assignFunc, ok := assignFuncMap[state.strategyType]
	if !ok {
		// should never happen at present
		return nil, fmt.Errorf("unsupported replica scheduling strategy, replicaSchedulingType: %s, replicaDivisionPreference: %s, "+
			"please try another scheduling strategy", spec.Placement.ReplicaSchedulingType(), spec.Placement.ReplicaScheduling.ReplicaDivisionPreference)
	}
	assignResults, err := assignFunc(state)
	if err != nil {
		return nil, err
	}
	return removeZeroReplicasCluster(assignResults), nil
}
