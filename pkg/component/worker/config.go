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

	"github.com/k0sproject/k0s/pkg/apis/k0s.k0sproject.io/v1beta1"
	"github.com/k0sproject/k0s/pkg/constant"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	kubeletv1beta1 "k8s.io/kubelet/config/v1beta1"

	"sigs.k8s.io/yaml"
)

type WorkerConfig interface {
	KubeletConfiguration() (kubeletv1beta1.KubeletConfiguration, error)
	NodeLocalLoadBalancer() (*v1beta1.NodeLocalLoadBalancer, error)
	DefaultImagePullPolicy() (corev1.PullPolicy, error)
	KonnectivityAgentPort() (uint16, error)
}

func LoadWorkerConfig(ctx context.Context, client kubernetes.Interface, profile string) (WorkerConfig, error) {
	cmName := fmt.Sprintf("%s-%s-%s", constant.WorkerConfigComponentName, profile, constant.KubernetesMajorMinorVersion)
	cm, err := client.CoreV1().ConfigMaps("kube-system").Get(ctx, cmName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	return &workerConfigMap{profile, cm.Data}, nil
}

type workerConfigMap struct {
	profile string
	data    map[string]string

	// FIXME: Implement validation
}

func (m *workerConfigMap) KubeletConfiguration() (kubeletv1beta1.KubeletConfiguration, error) {
	return unmarshal[kubeletv1beta1.KubeletConfiguration](m, "kubeletConfiguration")
}

func (m *workerConfigMap) NodeLocalLoadBalancer() (*v1beta1.NodeLocalLoadBalancer, error) {
	return unmarshalOpt[v1beta1.NodeLocalLoadBalancer](m, "nodeLocalLoadBalancer")
}

func (m *workerConfigMap) KonnectivityAgentPort() (uint16, error) {
	port, err := unmarshalOpt[uint16](m, "konnectivityAgentPort")
	if err != nil {
		return 0, err
	}
	if port != nil {
		return *port, nil
	}

	return 0, nil
}

func (m *workerConfigMap) DefaultImagePullPolicy() (corev1.PullPolicy, error) {
	return unmarshal[corev1.PullPolicy](m, "defaultImagePullPolicy")
}

func unmarshal[T any](m *workerConfigMap, key string) (t T, err error) {
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

func unmarshalOpt[T any](m *workerConfigMap, key string) (*T, error) {
	data, ok := m.data[key]
	if !ok {
		return nil, nil
	}

	t := new(T)
	err := yaml.Unmarshal([]byte(data), t)
	if err != nil {
		return nil, fmt.Errorf("failed to parse data for key %q in worker profile %q: %w", key, m.profile, err)
	}

	return t, nil
}
