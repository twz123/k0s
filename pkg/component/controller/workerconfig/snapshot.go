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
	"github.com/k0sproject/k0s/pkg/apis/k0s.k0sproject.io/v1beta1"

	"github.com/sirupsen/logrus"
)

// snapshot holds a snapshot of a cluster's worker config.
type snapshot struct {
	*configSnapshot
}

// configSnapshot holds a snapshot of the cluster configuration and the worker profiles.
type configSnapshot struct {
	profiles v1beta1.WorkerProfiles
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

func makeConfigSnapshot(log logrus.FieldLogger, spec *v1beta1.ClusterSpec) *configSnapshot {
	return &configSnapshot{
		profiles: spec.WorkerProfiles.DeepCopy(),
	}
}
