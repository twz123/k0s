/*
Copyright 2022 k0s authors

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

package workerconfig

import (
	"fmt"

	"github.com/k0sproject/k0s/pkg/apis/k0s.k0sproject.io/v1beta1"
	"github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
)

// snapshot holds a snapshot of a cluster's worker config.
type snapshot struct {
	apiServers
	*configSnapshot
}

// configSnapshot holds a snapshot of the cluster configuration and the worker profiles.
type configSnapshot struct {
	specSnapshot
	profiles v1beta1.WorkerProfiles
}

// specSnapshot holds a snapshot of the cluster configuration.
type specSnapshot struct {
	dnsAddress             string
	clusterDomain          string
	nodeLocalLoadBalancer  *v1beta1.NodeLocalLoadBalancer
	defaultImagePullPolicy corev1.PullPolicy
}

func (s *snapshot) DeepCopy() *snapshot {
	if s == nil {
		return nil
	}
	out := new(snapshot)
	s.DeepCopyInto(out)
	return out
}

func (s *snapshot) DeepCopyInto(out *snapshot) {
	*out = *s
	out.apiServers = s.apiServers.DeepCopy()
	out.configSnapshot = s.configSnapshot.DeepCopy()
}

func (s *configSnapshot) DeepCopy() *configSnapshot {
	if s == nil {
		return nil
	}
	out := new(configSnapshot)
	s.DeepCopyInto(out)
	return out
}

func (s *configSnapshot) DeepCopyInto(out *configSnapshot) {
	*out = *s
	s.profiles = out.profiles.DeepCopy()
}

func (s *snapshot) isComplete() bool {
	return len(s.apiServers) > 0 && s.configSnapshot != nil
}

func makeConfigSnapshot(log logrus.FieldLogger, spec *v1beta1.ClusterSpec) (*configSnapshot, error) {
	dnsAddress, err := spec.Network.DNSAddress()
	if err != nil {
		return nil, fmt.Errorf("failed to get DNS address from ClusterConfig: %w", err)
	}

	snap := &configSnapshot{
		specSnapshot: specSnapshot{
			dnsAddress:             dnsAddress,
			clusterDomain:          spec.Network.ClusterDomain,
			nodeLocalLoadBalancer:  spec.Network.NodeLocalLoadBalancer.DefaultedCopy(spec.Images),
			defaultImagePullPolicy: corev1.PullIfNotPresent,
		},
	}

	switch spec.Images.DefaultPullPolicy {
	case string(corev1.PullAlways):
		snap.defaultImagePullPolicy = corev1.PullAlways
	case string(corev1.PullNever):
		snap.defaultImagePullPolicy = corev1.PullNever
	case string(corev1.PullIfNotPresent):
		snap.defaultImagePullPolicy = corev1.PullIfNotPresent
	default:
		log.Warnf(
			"Ignoring unknown default image pull policy %q, continuing with %q",
			spec.Images.DefaultPullPolicy, snap.defaultImagePullPolicy,
		)
	}

	snap.profiles = spec.WorkerProfiles.DeepCopy()
	return snap, nil
}
