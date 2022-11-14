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

package config

import (
	"fmt"

	kubeletv1beta1 "k8s.io/kubelet/config/v1beta1"

	"sigs.k8s.io/yaml"
)

// Represents a source for k0s's worker configuration.
type Interface interface {
	// KubeletConfiguration provides the configuration to be passed to the k0s
	// worker's kubelet.
	KubeletConfiguration() (*kubeletv1beta1.KubeletConfiguration, error)
}

// configMap implements [Interface] by trying to load the configuration values
// on demand from the data in the worker-config ConfigMap.
type configMap struct {
	profileName string
	data        map[string]string
}

func (m *configMap) KubeletConfiguration() (*kubeletv1beta1.KubeletConfiguration, error) {
	var kubeletConfig *kubeletv1beta1.KubeletConfiguration
	err := unmarshal(m, "kubeletConfiguration", &kubeletConfig)
	if err != nil {
		return nil, err
	}
	return kubeletConfig, err
}

func unmarshal(m *configMap, key string, value any) error {
	data, ok := m.data[key]
	if !ok {
		return fmt.Errorf("no such key in profile %q: %q", m.profileName, key)
	}

	err := yaml.Unmarshal([]byte(data), value)
	if err != nil {
		return fmt.Errorf("failed to parse data for key %q in worker profile %q: %w", key, m.profileName, err)
	}

	return nil
}
