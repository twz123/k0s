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
	"context"
	"fmt"
	"testing"

	"github.com/k0sproject/k0s/pkg/constant"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	kubeletv1beta1 "k8s.io/kubelet/config/v1beta1"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoader(t *testing.T) {
	t.Parallel()

	const profileName = "fake-profile"
	workerConfigMap := corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "ConfigMap",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-%s-%s", constant.WorkerConfigComponentName, profileName, constant.KubernetesMajorMinorVersion),
			Namespace: "kube-system",
		},
		Data: map[string]string{"kubeletConfiguration": "{}"},
	}

	underTest := Loader{
		Kubeconfig: func() (*clientcmdapi.Config, error) {
			return &clientcmdapi.Config{
				Clusters: map[string]*clientcmdapi.Cluster{
					"fake": {Server: "127.99.99.1"},
				},
				Contexts: map[string]*clientcmdapi.Context{
					"fake": {Cluster: "fake"},
				},
				CurrentContext: "fake",
			}, nil
		},

		ClientFactory: func(c *rest.Config) (kubernetes.Interface, error) {
			availableObjects := []runtime.Object{&workerConfigMap}
			return fake.NewSimpleClientset(availableObjects...), nil
		},

		Log: func() logrus.FieldLogger {
			logger := logrus.New()
			logger.SetLevel(logrus.DebugLevel)
			return logger.WithField("test", t.Name())
		}(),
	}

	workerConfig, err := underTest.Load(context.TODO(), profileName)

	require.NoError(t, err)
	if assert.NotNil(t, workerConfig) {
		if kubeletConfig, err := workerConfig.KubeletConfiguration(); assert.NoError(t, err) {
			expected := &kubeletv1beta1.KubeletConfiguration{}
			assert.Equal(t, expected, kubeletConfig)
		}
	}
}
