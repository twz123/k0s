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
	"time"

	"github.com/k0sproject/k0s/pkg/constant"
	kubeutil "github.com/k0sproject/k0s/pkg/kubernetes"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/avast/retry-go"
	"github.com/sirupsen/logrus"
)

// Loader loads worker configurations from Kubernetes based on the worker
// profile name.
type Loader struct {
	// Used to load the kubeconfig to use when trying to load the worker config.
	Kubeconfig clientcmd.KubeconfigGetter

	// Optional function used to create the Kubernetes client from a given REST
	// config.
	ClientFactory func(*rest.Config) (kubernetes.Interface, error)

	// Logger to use during loading of the worker configuration. Defaults to the
	// standard logger if nil.
	Log logrus.FieldLogger
}

// Load loads the configuration with the given profile name from Kubernetes.
func (l *Loader) Load(ctx context.Context, profile string) (Interface, error) {
	log := l.Log
	if log == nil {
		log = logrus.StandardLogger()
	}

	var cm *configMap
	if err := retry.Do(
		func() error {
			clientConfig, err := kubeutil.ClientConfig(l.Kubeconfig)
			if err != nil {
				return fmt.Errorf("failed to create Kubernetes client config to load worker configuration: %w", err)
			}

			cm, err = l.loadWorkerConfig(ctx, clientConfig, profile)
			return err
		},
		retry.Context(ctx),
		retry.LastErrorOnly(true),
		retry.Delay(500*time.Millisecond),
		retry.OnRetry(func(attempt uint, err error) {
			log.WithError(err).Debugf("Failed to load configuration for worker profile in attempt #%d, retrying after backoff", attempt+1)
		}),
	); err != nil {
		if apierrors.IsUnauthorized(err) {
			err = fmt.Errorf("the k0s worker node credentials are invalid, the node needs to be rejoined into the cluster with a fresh bootstrap token: %w", err)
		}

		return nil, err
	}

	return cm, nil
}

func (l *Loader) newClientForConfig(restConfig *rest.Config) (kubernetes.Interface, error) {
	if l.ClientFactory != nil {
		return l.ClientFactory(restConfig)
	}

	return kubernetes.NewForConfig(restConfig)
}

func (l *Loader) loadWorkerConfig(ctx context.Context, restConfig *rest.Config, profile string) (*configMap, error) {
	configMapName := fmt.Sprintf("%s-%s-%s", constant.WorkerConfigComponentName, profile, constant.KubernetesMajorMinorVersion)

	client, err := l.newClientForConfig(restConfig)
	if err != nil {
		return nil, err
	}

	cm, err := client.CoreV1().ConfigMaps("kube-system").Get(ctx, configMapName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	return &configMap{profile, cm.Data}, nil
}
