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

package common

import (
	"context"
	"time"

	"github.com/k0sproject/k0s/pkg/kubernetes/watch"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
	aggregatorclient "k8s.io/kube-aggregator/pkg/client/clientset_generated/clientset"
)

// Poll tries a condition func until it returns true, an error or the specified
// context is canceled or expired.
func Poll(ctx context.Context, condition wait.ConditionWithContextFunc) error {
	return wait.PollImmediateUntilWithContext(ctx, 100*time.Millisecond, condition)
}

func fallbackContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.TODO(), 5*time.Minute)
}

func fallbackPoll(condition wait.ConditionWithContextFunc) error {
	ctx, cancel := fallbackContext()
	defer cancel()
	return Poll(ctx, condition)
}

// WaitForCalicoReady waits to see all calico pods healthy
func WaitForCalicoReady(kc *kubernetes.Clientset) error {
	return WaitForDaemonSet(kc, "calico-node")
}

// WaitForKubeRouterReady waits to see all kube-router pods healthy.
func WaitForKubeRouterReady(kc *kubernetes.Clientset) error {
	return fallbackPoll(waitForKubeRouterReady(kc))
}

// WaitForKubeRouterReady waits to see all kube-router pods healthy as long as
// the context isn't canceled.
func WaitForKubeRouterReadyWithContext(ctx context.Context, kc *kubernetes.Clientset) error {
	return Poll(ctx, waitForKubeRouterReady(kc))
}

func waitForKubeRouterReady(kc *kubernetes.Clientset) wait.ConditionWithContextFunc {
	return waitForDaemonSet(kc, "kube-router")
}

func WaitForMetricsReady(ctx context.Context, c *rest.Config) error {
	clientset, err := aggregatorclient.NewForConfig(c)
	if err != nil {
		return err
	}

	type (
		service     = apiregistrationv1.APIService
		serviceList = apiregistrationv1.APIServiceList
	)

	client := clientset.ApiregistrationV1().APIServices()
	return watch.FromClient[*serviceList, service](client).
		WithObjectName("v1beta1.metrics.k8s.io").
		Until(ctx, func(service *service) (bool, error) {
			for _, c := range service.Status.Conditions {
				if c.Type == "Available" && c.Status == apiregistrationv1.ConditionTrue {
					return true, nil
				}
			}

			return false, nil
		})
}

// WaitForDaemonSet waits for daemon set be ready.
func WaitForDaemonSet(kc *kubernetes.Clientset, name string) error {
	return fallbackPoll(waitForDaemonSet(kc, name))
}

// WaitForDaemonSetWithContext waits for daemon set be ready as long as the
// given context isn't canceled.
func WaitForDaemonSetWithContext(ctx context.Context, kc *kubernetes.Clientset, name string) error {
	return Poll(ctx, waitForDaemonSet(kc, name))
}

func waitForDaemonSet(kc *kubernetes.Clientset, name string) wait.ConditionWithContextFunc {
	return func(ctx context.Context) (done bool, err error) {
		ds, err := kc.AppsV1().DaemonSets("kube-system").Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, nil
		}

		return ds.Status.NumberAvailable == ds.Status.DesiredNumberScheduled, nil
	}
}

// WaitForDeployment waits for a deployment to become ready.
func WaitForDeployment(kc *kubernetes.Clientset, name string) error {
	return fallbackPoll(waitForDeployment(kc, name))
}

// WaitForDeploymentWithContext waits for a deployment to become ready as long
// as the given context isn't canceled.
func WaitForDeploymentWithContext(ctx context.Context, kc *kubernetes.Clientset, name string) error {
	return Poll(ctx, waitForDeployment(kc, name))
}

func waitForDeployment(kc *kubernetes.Clientset, name string) wait.ConditionWithContextFunc {
	return func(ctx context.Context) (done bool, err error) {
		dep, err := kc.AppsV1().Deployments("kube-system").Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, nil
		}

		return *dep.Spec.Replicas == dep.Status.ReadyReplicas, nil
	}
}

// WaitForPod waits for the given pod to become ready as long as the given
// context isn't canceled.
func WaitForPod(ctx context.Context, kc *kubernetes.Clientset, name, namespace string) error {
	return watch.Pods(kc.CoreV1().Pods(namespace)).
		WithObjectName(name).
		Until(ctx, func(pod *corev1.Pod) (bool, error) {
			for _, cond := range pod.Status.Conditions {
				if cond.Type == corev1.PodReady {
					return cond.Status == corev1.ConditionTrue, nil
				}
			}

			return false, nil
		})
}

// WaitForPodLogs waits until it can stream the logs of any running pod in the
// given namespace as long as the given context isn't canceled.
func WaitForPodLogs(ctx context.Context, kc *kubernetes.Clientset, namespace string) error {
	pods := kc.CoreV1().Pods(namespace)
	return watch.Pods(pods).
		WithStatusPhase(string(corev1.PodRunning)).
		Until(ctx, func(pod *corev1.Pod) (bool, error) {
			opts := corev1.PodLogOptions{Container: pod.Spec.Containers[0].Name}
			logs, err := pods.GetLogs(pod.Name, &opts).Stream(ctx)
			if err != nil {
				return false, nil // do not return the error so we keep on polling
			}
			return true, logs.Close()
		})
}

func WaitForLease(ctx context.Context, kc *kubernetes.Clientset, name string, namespace string) error {
	return Poll(ctx, func(ctx context.Context) (done bool, err error) {
		lease, err := kc.CoordinationV1().Leases(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil && apierrors.IsNotFound(err) {
			return false, nil // Not found, keep polling
		} else if err != nil {
			return false, err
		}

		// Verify that there's a valid holder on the lease
		return *lease.Spec.HolderIdentity != "", nil
	})
}
