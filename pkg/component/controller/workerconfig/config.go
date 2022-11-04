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

	"github.com/k0sproject/k0s/pkg/applier"
	"github.com/k0sproject/k0s/pkg/constant"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubeletv1beta1 "k8s.io/kubelet/config/v1beta1"

	"sigs.k8s.io/yaml"
)

// workerConfig represents the worker config for a given profile.
type workerConfig struct {
	kubeletConfiguration kubeletv1beta1.KubeletConfiguration
}

func (c *workerConfig) toConfigMap(name string) (*corev1.ConfigMap, error) {
	kubeletConfig, err := yaml.Marshal(c.kubeletConfiguration)
	if err != nil {
		return nil, err
	}

	data := map[string]string{
		"kubeletConfiguration": string(kubeletConfig),
	}

	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-%s-%s", constant.WorkerConfigComponentName, name, constant.KubernetesMajorMinorVersion),
			Namespace: "kube-system",
			Labels: applier.
				CommonLabels(constant.WorkerConfigComponentName).
				With("k0s.k0sproject.io/worker-profile", name),
		},
		Data: data,
	}, nil
}
