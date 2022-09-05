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
	DefaultImagePullPolicy() (corev1.PullPolicy, error)
	EnvoyProxyImage() (v1beta1.ImageSpec, error)
	KubeletConfiguration() (kubeletv1beta1.KubeletConfiguration, error)
}

func LoadWorkerConfig(ctx context.Context, client kubernetes.Interface, profile string) (WorkerConfig, error) {
	cmName := fmt.Sprintf("%s-%s-%s", constant.WorkerConfigComponentName, profile, constant.KubernetesMajorMinorVersion)
	cm, err := client.CoreV1().ConfigMaps("kube-system").Get(ctx, cmName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	keyData := func(key string) (string, error) {
		data, ok := cm.Data[key]
		if !ok {
			return "", fmt.Errorf("no key named %q in ConfigMap %s/%s", key, cm.Namespace, cm.Name)
		}
		return data, nil
	}

	return &workerConfigFuncs{
		defaultImagePullPolicy: unmarshal[corev1.PullPolicy]("defaultImagePullPolicy", keyData),
		envoyProxyImage:        unmarshal[v1beta1.ImageSpec]("envoyProxyImage", keyData),
		kubeletConfiguration:   unmarshal[kubeletv1beta1.KubeletConfiguration]("kubeletConfiguration", keyData),
	}, nil
}

type workerConfigFuncs struct {
	defaultImagePullPolicy func() (corev1.PullPolicy, error)
	envoyProxyImage        func() (v1beta1.ImageSpec, error)
	kubeletConfiguration   func() (kubeletv1beta1.KubeletConfiguration, error)
}

func (f *workerConfigFuncs) DefaultImagePullPolicy() (corev1.PullPolicy, error) {
	return f.defaultImagePullPolicy()
}

func (f *workerConfigFuncs) EnvoyProxyImage() (v1beta1.ImageSpec, error) {
	return f.envoyProxyImage()
}

func (f *workerConfigFuncs) KubeletConfiguration() (kubeletv1beta1.KubeletConfiguration, error) {
	return f.kubeletConfiguration()
}

func unmarshal[T any](key string, getData func(string) (string, error)) func() (T, error) {
	return func() (t T, err error) {
		data, err := getData(key)
		if err != nil {
			return t, err
		}
		err = yaml.Unmarshal([]byte(data), &t)
		return
	}
}
