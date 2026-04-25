/*
Copyright 2024 The Karmada Authors.

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

package util

import policyv1alpha1 "github.com/karmada-io/karmada/pkg/apis/policy/v1alpha1"

// IsLazyActivationEnabled judge whether lazy activation preference is enabled.
func IsLazyActivationEnabled(activationPreference policyv1alpha1.ActivationPreference) bool {
	if activationPreference == "" {
		return false
	}
	return activationPreference == policyv1alpha1.LazyActivation
}

// IsOverflowSupport judge whether the replica scheduling strategy supports overflow.
func IsOverflowSupport(replicaScheduling *policyv1alpha1.ReplicaSchedulingStrategy) bool {
	if replicaScheduling == nil || replicaScheduling.ReplicaSchedulingType != policyv1alpha1.ReplicaSchedulingTypeDivided {
		return false
	}

	if replicaScheduling.ReplicaDivisionPreference == policyv1alpha1.ReplicaDivisionPreferenceWeighted &&
		(replicaScheduling.WeightPreference == nil ||
			len(replicaScheduling.WeightPreference.StaticWeightList) != 0 && replicaScheduling.WeightPreference.DynamicWeight == "") {
		return false
	}

	return true
}
