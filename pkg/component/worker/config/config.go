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

package worker

import (
	"context"
	"fmt"

	"github.com/k0sproject/k0s/pkg/constant"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	kubeletv1beta1 "k8s.io/kubelet/config/v1beta1"

	"sigs.k8s.io/yaml"
)

type Interface interface {
	KubeletConfiguration() (kubeletv1beta1.KubeletConfiguration, error)
}

func Load(ctx context.Context, client kubernetes.Interface, profile string) (Interface, error) {
	cmName := fmt.Sprintf("%s-%s-%s", constant.WorkerConfigComponentName, profile, constant.KubernetesMajorMinorVersion)
	cm, err := client.CoreV1().ConfigMaps("kube-system").Get(ctx, cmName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	return &configMap{profile, cm.Data}, nil
}

type configMap struct {
	profile string
	data    map[string]string
}

func (m *configMap) KubeletConfiguration() (kubeletv1beta1.KubeletConfiguration, error) {
	return unmarshal[kubeletv1beta1.KubeletConfiguration](m, "kubeletConfiguration")
}

func unmarshal[T any](m *configMap, key string) (t T, err error) {
	data, ok := m.data[key]
	if !ok {
		return t, fmt.Errorf("no such key in profile %q: %q", m.profile, key)
	}

	err = yaml.Unmarshal([]byte(data), &t)
	if err != nil {
		err = fmt.Errorf("failed to parse data for key %q in worker profile %q: %w", key, m.profile, err)
	}

	return
}
