/*
Copyright 2022 The Karmada Authors.

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

package pb

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

// SetNodeAffinity marshals the given NodeSelector into both NodeAffinity and NodeAffinityBytes fields.
func (m *NodeClaim) SetNodeAffinity(s *corev1.NodeSelector) error {
	if s == nil {
		m.NodeAffinity = nil
		m.NodeAffinityBytes = nil
		return nil
	}
	b, err := s.Marshal()
	if err != nil {
		return err
	}
	m.NodeAffinity = b
	m.NodeAffinityBytes = b
	return nil
}

// UnmarshalNodeAffinity unmarshals the NodeAffinityBytes field (or NodeAffinity field as fallback) into a NodeSelector.
func (m *NodeClaim) UnmarshalNodeAffinity() (*corev1.NodeSelector, error) {
	data := m.NodeAffinityBytes
	if len(data) == 0 {
		data = m.NodeAffinity
	}
	if len(data) == 0 {
		return nil, nil
	}
	s := &corev1.NodeSelector{}
	if err := s.Unmarshal(data); err != nil {
		return nil, err
	}
	return s, nil
}

// SetTolerations marshals the given Tolerations into both Tolerations and TolerationsBytes fields.
func (m *NodeClaim) SetTolerations(ts []corev1.Toleration) error {
	if ts == nil {
		m.Tolerations = nil
		m.TolerationsBytes = nil
		return nil
	}
	m.Tolerations = make([][]byte, len(ts))
	m.TolerationsBytes = make([][]byte, len(ts))
	for i, t := range ts {
		b, err := t.Marshal()
		if err != nil {
			return err
		}
		m.Tolerations[i] = b
		m.TolerationsBytes[i] = b
	}
	return nil
}

// UnmarshalTolerations unmarshals the TolerationsBytes field (or Tolerations field as fallback) into a slice of Tolerations.
func (m *NodeClaim) UnmarshalTolerations() ([]corev1.Toleration, error) {
	data := m.TolerationsBytes
	if len(data) == 0 {
		data = m.Tolerations
	}
	if len(data) == 0 {
		return nil, nil
	}
	ts := make([]corev1.Toleration, len(data))
	for i, b := range data {
		if err := ts[i].Unmarshal(b); err != nil {
			return nil, err
		}
	}
	return ts, nil
}

// SetResourceRequest marshals the given ResourceList into both ResourceRequest and ResourceRequestBytes fields.
func (m *ReplicaRequirements) SetResourceRequest(res corev1.ResourceList) error {
	if res == nil {
		m.ResourceRequest = nil
		m.ResourceRequestBytes = nil
		return nil
	}
	m.ResourceRequest = make(map[string][]byte)
	m.ResourceRequestBytes = make(map[string][]byte)
	for k, v := range res {
		b, err := v.Marshal()
		if err != nil {
			return err
		}
		m.ResourceRequest[string(k)] = b
		m.ResourceRequestBytes[string(k)] = b
	}
	return nil
}

// UnmarshalResourceRequest unmarshals the ResourceRequestBytes field (or ResourceRequest field as fallback) into a ResourceList.
func (m *ReplicaRequirements) UnmarshalResourceRequest() (corev1.ResourceList, error) {
	data := m.ResourceRequestBytes
	if len(data) == 0 {
		data = m.ResourceRequest
	}
	if len(data) == 0 {
		return nil, nil
	}
	res := make(corev1.ResourceList)
	for k, v := range data {
		q := resource.Quantity{}
		if err := q.Unmarshal(v); err != nil {
			return nil, err
		}
		res[corev1.ResourceName(k)] = q
	}
	return res, nil
}

// SetResourceRequest marshals the given ResourceList into both ResourceRequest and ResourceRequestBytes fields.
func (m *ComponentReplicaRequirements) SetResourceRequest(res corev1.ResourceList) error {
	if res == nil {
		m.ResourceRequest = nil
		m.ResourceRequestBytes = nil
		return nil
	}
	m.ResourceRequest = make(map[string][]byte)
	m.ResourceRequestBytes = make(map[string][]byte)
	for k, v := range res {
		b, err := v.Marshal()
		if err != nil {
			return err
		}
		m.ResourceRequest[string(k)] = b
		m.ResourceRequestBytes[string(k)] = b
	}
	return nil
}

// UnmarshalResourceRequest unmarshals the ResourceRequestBytes field (or ResourceRequest field as fallback) into a ResourceList.
func (m *ComponentReplicaRequirements) UnmarshalResourceRequest() (corev1.ResourceList, error) {
	data := m.ResourceRequestBytes
	if len(data) == 0 {
		data = m.ResourceRequest
	}
	if len(data) == 0 {
		return nil, nil
	}
	res := make(corev1.ResourceList)
	for k, v := range data {
		q := resource.Quantity{}
		if err := q.Unmarshal(v); err != nil {
			return nil, err
		}
		res[corev1.ResourceName(k)] = q
	}
	return res, nil
}

// MustSetNodeAffinity sets node affinity and panics on error.
func (m *NodeClaim) MustSetNodeAffinity(s *corev1.NodeSelector) *NodeClaim {
	if err := m.SetNodeAffinity(s); err != nil {
		panic(err)
	}
	return m
}

// MustSetTolerations sets tolerations and panics on error.
func (m *NodeClaim) MustSetTolerations(ts []corev1.Toleration) *NodeClaim {
	if err := m.SetTolerations(ts); err != nil {
		panic(err)
	}
	return m
}

// MustSetResourceRequest sets resource request and panics on error.
func (m *ReplicaRequirements) MustSetResourceRequest(res corev1.ResourceList) *ReplicaRequirements {
	if err := m.SetResourceRequest(res); err != nil {
		panic(err)
	}
	return m
}

// MustSetResourceRequest sets resource request and panics on error.
func (m *ComponentReplicaRequirements) MustSetResourceRequest(res corev1.ResourceList) *ComponentReplicaRequirements {
	if err := m.SetResourceRequest(res); err != nil {
		panic(err)
	}
	return m
}
