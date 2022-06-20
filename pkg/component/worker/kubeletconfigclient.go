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
	"errors"
	"fmt"

	"github.com/k0sproject/k0s/pkg/constant"
	k8sutil "github.com/k0sproject/k0s/pkg/kubernetes"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	watchtools "k8s.io/client-go/tools/watch"

	"github.com/sirupsen/logrus"
)

// KubeletConfigClient is the client used to fetch kubelet config from a common config map
type KubeletConfigClient struct {
	kubeClient kubernetes.Interface
}

// NewKubeletConfigClient creates new KubeletConfigClient using the specified kubeconfig
func NewKubeletConfigClient(kubeconfigPath string) (*KubeletConfigClient, error) {
	kubeClient, err := k8sutil.NewClient(kubeconfigPath)
	if err != nil {
		return nil, err
	}

	return &KubeletConfigClient{
		kubeClient: kubeClient,
	}, nil
}

// Get reads the config from kube api
func (k *KubeletConfigClient) Get(ctx context.Context, profile string) (string, error) {
	cmName := fmt.Sprintf("kubelet-config-%s-%s", profile, constant.KubernetesMajorMinorVersion)
	cm, err := k.kubeClient.CoreV1().ConfigMaps("kube-system").Get(ctx, cmName, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to get kubelet config from API: %w", err)
	}
	config := cm.Data["kubelet"]
	if config == "" {
		return "", fmt.Errorf("no config found with key 'kubelet' in %s", cmName)
	}
	return config, nil
}

func (k *KubeletConfigClient) WatchAPIServers(ctx context.Context, callback func(*corev1.Endpoints) error) error {
	endpoints := k.kubeClient.CoreV1().Endpoints("default")
	fieldSelector := fields.OneTermEqualSelector(metav1.ObjectNameField, "kubernetes").String()

	initial, err := endpoints.List(ctx, metav1.ListOptions{
		FieldSelector: fieldSelector,
	})
	if err != nil {
		return err
	}
	if len(initial.Items) == 1 {
		if err := callback(&initial.Items[0]); err != nil {
			return err
		}
	} else {
		logrus.Debugf("Didn't find exactly one Endpoints object for API servers, but %d", len(initial.Items))
	}

	changes, err := watchtools.NewRetryWatcher(initial.ResourceVersion, &cache.ListWatch{
		WatchFunc: func(opts metav1.ListOptions) (watch.Interface, error) {
			opts.FieldSelector = fieldSelector
			return endpoints.Watch(ctx, opts)
		},
	})
	if err != nil {
		return err
	}
	defer changes.Stop()

	for {
		select {
		case event, ok := <-changes.ResultChan():
			if !ok {
				return errors.New("result channel closed unexpectedly")
			}

			switch event.Type {
			case watch.Added, watch.Modified:
				logrus.Infof("Changes to API servers: %s %#+v", event.Type, event.Object)
			case watch.Deleted:
				logrus.Infof("Changes to API servers: %s %#+v", event.Type, event.Object)
			case watch.Bookmark:
				logrus.Infof("Changes to API servers: %s %#+v", event.Type, event.Object)
			case watch.Error:
				logrus.WithError(apierrors.FromObject(event.Object)).Error("Error while watching API server endpoints")
			}

		case <-ctx.Done():
			return nil
		}
	}
}
