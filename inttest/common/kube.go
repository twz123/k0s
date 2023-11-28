/*
Copyright 2020 k0s authors

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
	"errors"
	"fmt"
	"io"
	"syscall"
	"testing"
	"time"

	k0sclientset "github.com/k0sproject/k0s/pkg/client/clientset"
	"github.com/k0sproject/k0s/pkg/k0scontext"
	"github.com/k0sproject/k0s/pkg/kubernetes/watch"

	appsv1 "k8s.io/api/apps/v1"
	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	extclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
	aggregatorclient "k8s.io/kube-aggregator/pkg/client/clientset_generated/clientset"

	"github.com/sirupsen/logrus"
)

// LogfFn will be used whenever something needs to be logged.
type LogfFn func(format string, args ...any)

// Poll is a utility function to check for a condition at regular intervals. It
// repeatedly executes a condition function until it returns true, encounters an
// error, or the provided context is canceled or expires.
func Poll(ctx context.Context, condition wait.ConditionWithContextFunc) error {
	return wait.PollUntilContextCancel(ctx, 100*time.Millisecond, true, condition)
}

type KubeAPI struct {
	client    kubernetes.Interface
	k0sclient k0sclientset.Interface
	extclient extclient.Interface
	aggclient aggregatorclient.Interface
}

func newKubeAPI(config *rest.Config) (*KubeAPI, error) {
	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	k0sclient, err := k0sclientset.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	extclient, err := extclient.NewForConfig(config)
	if err != nil {
		return nil, err
	}
	aggclient, err := aggregatorclient.NewForConfig(config)
	if err != nil {
		return nil, err
	}

	return &KubeAPI{client, k0sclient, extclient, aggclient}, nil
}

// WaitForNodeReady waits until the Kubernetes node with the given name reaches
// the Ready condition, it encounters an error, or the provided context is
// canceled or expires.
func (k *KubeAPI) WaitForNodeReady(ctx context.Context, name string) error {
	logf := logfFrom(ctx)
	logf("Waiting for node %s to become ready", name)
	if err := WaitForNodeReadyStatus(ctx, k.client, name, corev1.ConditionTrue); err != nil {
		return err
	}
	logf("Node %s is ready", name)
	return nil
}

// WaitForNodeReadyStatus waits until the Ready condition of the Kubernetes node
// with the given name reaches the given status, it encounters an error, or the
// provided context is canceled or expires.
func (k *KubeAPI) WaitForNodeReadyStatus(ctx context.Context, name string, status corev1.ConditionStatus) error {
	return k.watchNodeUntil(ctx, name, func(node *corev1.Node) (done bool, err error) {
		for _, cond := range node.Status.Conditions {
			if cond.Type == corev1.NodeReady {
				if cond.Status == status {
					return true, nil
				}

				break
			}
		}

		return false, nil
	})
}

// WaitForNodeLabel waits until the specified label is assigned the given value
// to the Kubernetes node with the given name, it encounters an error, or the
// provided context is canceled or expires.
func (k *KubeAPI) WaitForNodeLabel(ctx context.Context, name, key, value string) error {
	return k.watchNodeUntil(ctx, name, func(node *corev1.Node) (done bool, err error) {
		for k, v := range node.Labels {
			if key == k {
				if value == v {
					return true, nil
				}

				break
			}
		}

		return false, nil
	})
}

func (k *KubeAPI) watchNodeUntil(ctx context.Context, name string, condition func(*corev1.Node) (done bool, err error)) error {
	return watch.Nodes(k.client.CoreV1().Nodes()).
		WithObjectName(name).
		WithErrorCallback(RetryWatchErrors(logfFrom(ctx))).
		Until(ctx, condition)
}

// WaitForKubeRouterReady waits until k0s's Kube-Router component is
// operational, it encounters an error, or the provided context is canceled or
// expires.
func (k *KubeAPI) WaitForKubeRouterReady(ctx context.Context) error {
	return k.WaitForDaemonSet(ctx, "kube-router")
}

// WaitForCoreDNSReady waits until k0s's CoreDNS component is operational, it
// encounters an error, or the provided context is canceled or expires. It
// checks for the Deployment to be ready as well as all the related endpoints to
// be ready.
func (k *KubeAPI) WaitForCoreDNSReady(ctx context.Context) error {
	err := k.WaitForDeployment(ctx, "coredns", "kube-system")
	if err != nil {
		return fmt.Errorf("while waiting for CoreDNS Deployment: %w", err)
	}

	watchEndpointSlices := watch.FromClient[*discoveryv1.EndpointSliceList, discoveryv1.EndpointSlice]
	err = watchEndpointSlices(k.client.DiscoveryV1().EndpointSlices("kube-system")).
		WithLabels(labels.Set{"k8s-app": "kube-dns"}).
		WithErrorCallback(RetryWatchErrors(logrus.Infof)).
		Until(ctx, func(slice *discoveryv1.EndpointSlice) (bool, error) {
			// Check that all endpoints show ready conditions
			var numReadyEndpoints, numUneadyEndpoints uint
			for _, endpoint := range slice.Endpoints {
				ready := endpoint.Conditions.Ready
				if ready != nil && *ready {
					numReadyEndpoints++
				} else {
					numUneadyEndpoints++
				}
			}

			return numReadyEndpoints > 0 && numUneadyEndpoints == 0, nil
		})
	if err != nil {
		return fmt.Errorf("while waiting for CoreDNS EndpointSlices: %w", err)
	}

	return nil
}

// WaitForMetricsReady waits until k0s's metrics-server component is
// operational, it encounters an error, or the provided context is canceled or
// expires.
func (k *KubeAPI) WaitForMetricsReady(ctx context.Context) error {
	watchAPIServices := watch.FromClient[*apiregistrationv1.APIServiceList, apiregistrationv1.APIService]
	return watchAPIServices(k.aggclient.ApiregistrationV1().APIServices()).
		WithObjectName("v1beta1.metrics.k8s.io").
		WithErrorCallback(RetryWatchErrors(logfFrom(ctx))).
		Until(ctx, func(service *apiregistrationv1.APIService) (bool, error) {
			for _, c := range service.Status.Conditions {
				if c.Type == apiregistrationv1.Available {
					if c.Status == apiregistrationv1.ConditionTrue {
						return true, nil
					}

					break
				}
			}

			return false, nil
		})
}

// WaitForDaemonSet waits until the specified DaemonSet has the expected number
// of ready replicas as defined in its specification, it encounters an error, or
// the provided context is canceled or expires.
func (k *KubeAPI) WaitForDaemonSet(ctx context.Context, name string) error {
	return watch.DaemonSets(k.client.AppsV1().DaemonSets("kube-system")).
		WithObjectName(name).
		WithErrorCallback(RetryWatchErrors(logfFrom(ctx))).
		Until(ctx, func(ds *appsv1.DaemonSet) (bool, error) {
			return ds.Status.NumberAvailable == ds.Status.DesiredNumberScheduled, nil
		})
}

// WaitForDeployment waits until the specified Deployment to become available,
// it encounters an error, or the provided context is canceled or expires.
func (k *KubeAPI) WaitForDeployment(ctx context.Context, name, namespace string) error {
	return watch.Deployments(k.client.AppsV1().Deployments(namespace)).
		WithObjectName(name).
		WithErrorCallback(RetryWatchErrors(logfFrom(ctx))).
		Until(ctx, func(deployment *appsv1.Deployment) (bool, error) {
			for _, c := range deployment.Status.Conditions {
				if c.Type == appsv1.DeploymentAvailable {
					if c.Status == corev1.ConditionTrue {
						return true, nil
					}

					break
				}
			}

			return false, nil
		})
}

// WaitForStatefulSet waits until the specified StatefulSet has the expected
// number of ready replicas as defined in its specification, it encounters an
// error, or the provided context is canceled or expires.
func (k *KubeAPI) WaitForStatefulSet(ctx context.Context, name, namespace string) error {
	return watch.StatefulSets(k.client.AppsV1().StatefulSets(namespace)).
		WithObjectName(name).
		WithErrorCallback(RetryWatchErrors(logfFrom(ctx))).
		Until(ctx, func(s *appsv1.StatefulSet) (bool, error) {
			return s.Status.ReadyReplicas == *s.Spec.Replicas, nil
		})
}

// WaitForPod waits for the specified pod to reach its Ready condition, it
// encounters an error, or the provided context is canceled or expires.
func (k *KubeAPI) WaitForPod(ctx context.Context, name, namespace string) error {
	return watch.Pods(k.client.CoreV1().Pods(namespace)).
		WithObjectName(name).
		WithErrorCallback(RetryWatchErrors(logfFrom(ctx))).
		Until(ctx, func(pod *corev1.Pod) (bool, error) {
			for _, cond := range pod.Status.Conditions {
				if cond.Type == corev1.PodReady {
					if cond.Status == corev1.ConditionTrue {
						return true, nil
					}

					break
				}
			}

			return false, nil
		})
}

// WaitForPodLogs waits until it can stream the logs of an arbitrary running
// pod in a given namespace, it encounters an error, or the provided context is
// canceled or expires. This is useful to test if k0s's konnectivity component
// is operational.
func (k *KubeAPI) WaitForPodLogs(ctx context.Context, namespace string) error {
	return Poll(ctx, func(ctx context.Context) (done bool, err error) {
		pods, err := k.client.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
			Limit:         1,
			FieldSelector: fields.OneTermEqualSelector("status.phase", string(corev1.PodRunning)).String(),
		})
		if err != nil {
			return false, err // stop polling with error in case the pod listing fails
		}
		if len(pods.Items) < 1 {
			return false, nil
		}

		pod := &pods.Items[0]
		logs, err := k.client.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name, &corev1.PodLogOptions{Container: pod.Spec.Containers[0].Name}).Stream(ctx)
		if err != nil {
			return false, nil // do not return the error so we keep on polling
		}
		defer logs.Close()

		return true, nil
	})
}

// WaitForLease waits for a specific Kubernetes lease to be acquired, it
// encounters an error, or the provided context is canceled or expires. It
// returns the holder identity of the lease on success.
func (k *KubeAPI) WaitForLease(ctx context.Context, name string, namespace string) (string, error) {
	var holderIdentity string
	watchLeases := watch.FromClient[*coordinationv1.LeaseList, coordinationv1.Lease]
	err := watchLeases(k.client.CoordinationV1().Leases(namespace)).
		WithObjectName(name).
		WithErrorCallback(RetryWatchErrors(logfFrom(ctx))).
		Until(ctx, func(lease *coordinationv1.Lease) (bool, error) {
			holderIdentity = *lease.Spec.HolderIdentity
			// Verify that there's a valid holder on the lease
			return holderIdentity != "", nil
		})
	if err != nil {
		return "", err
	}

	return holderIdentity, nil
}

// VerifySomeKubeSystemPods checks for the presence of some pods in the
// kube-system namespace.
func (k *KubeAPI) VerifySomeKubeSystemPods(ctx context.Context) error {
	pods, err := k.client.CoreV1().Pods("kube-system").List(ctx, metav1.ListOptions{
		Limit: 100,
	})
	if err != nil {
		return fmt.Errorf("while listing pods in kube-system namespace: %w", err)
	}
	if len(pods.Items) <= 1 {
		return errors.New("no pods found in kube-system namespace")
	}

	return nil
}

// WaitForNodeReady waits until the Kubernetes node with the given name reaches
// the Ready condition, it encounters an error, or the provided context is
// canceled or expires.
//
// Deprecated: Use [KubeAPI] instead.
func WaitForNodeReady(ctx context.Context, client kubernetes.Interface, name string) error {
	return (&KubeAPI{client: client}).WaitForNodeReady(ctx, name)
}

// WaitForNodeReadyStatus waits until the Ready condition of the Kubernetes node
// with the given name reaches the given status, it encounters an error, or the
// provided context is canceled or expires.
//
// Deprecated: Use [KubeAPI] instead.
func WaitForNodeReadyStatus(ctx context.Context, client kubernetes.Interface, name string, status corev1.ConditionStatus) error {
	return (&KubeAPI{client: client}).WaitForNodeReadyStatus(ctx, name, status)
}

// WaitForNodeLabel waits until the specified label is assigned the given value
// to the Kubernetes node with the given name, it encounters an error, or the
// provided context is canceled or expires.
//
// Deprecated: Use [KubeAPI] instead.
func WaitForNodeLabel(ctx context.Context, client kubernetes.Interface, name, key, value string) error {
	return (&KubeAPI{client: client}).WaitForNodeLabel(ctx, name, key, value)
}

// WaitForKubeRouterReady waits until k0s's Kube-Router component is
// operational, it encounters an error, or the provided context is canceled or
// expires.
//
// Deprecated: Use [KubeAPI] instead.
func WaitForKubeRouterReady(ctx context.Context, kc *kubernetes.Clientset) error {
	return (&KubeAPI{client: kc}).WaitForKubeRouterReady(ctx)
}

// WaitForCoreDNSReady waits until k0s's CoreDNS component is operational, it
// encounters an error, or the provided context is canceled or expires. It
// checks for the Deployment to be ready as well as all the related endpoints to
// be ready.
//
// Deprecated: Use [KubeAPI] instead.
func WaitForCoreDNSReady(ctx context.Context, client kubernetes.Interface) error {
	return (&KubeAPI{client: client}).WaitForCoreDNSReady(ctx)
}

// WaitForMetricsReady waits until k0s's metrics-server component is
// operational, it encounters an error, or the provided context is canceled or
// expires.
//
// Deprecated: Use [KubeAPI] instead.
func WaitForMetricsReady(ctx context.Context, c *rest.Config) error {
	k, err := newKubeAPI(c)
	if err != nil {
		return err
	}
	return k.WaitForMetricsReady(ctx)
}

// WaitForDaemonSet waits until the specified DaemonSet has the expected number
// of ready replicas as defined in its specification, it encounters an error, or
// the provided context is canceled or expires.
//
// Deprecated: Use [KubeAPI] instead.
func WaitForDaemonSet(ctx context.Context, kc *kubernetes.Clientset, name string) error {
	return (&KubeAPI{client: kc}).WaitForDaemonSet(ctx, name)
}

// WaitForDeployment waits until the specified Deployment to become available,
// it encounters an error, or the provided context is canceled or expires.
//
// Deprecated: Use [KubeAPI] instead.
func WaitForDeployment(ctx context.Context, client kubernetes.Interface, name, namespace string) error {
	return (&KubeAPI{client: client}).WaitForDeployment(ctx, name, namespace)
}

// WaitForStatefulSet waits until the specified StatefulSet has the expected
// number of ready replicas as defined in its specification, it encounters an
// error, or the provided context is canceled or expires.
//
// Deprecated: Use [KubeAPI] instead.
func WaitForStatefulSet(ctx context.Context, kc *kubernetes.Clientset, name, namespace string) error {
	return (&KubeAPI{client: kc}).WaitForStatefulSet(ctx, name, namespace)
}

// WaitForPod waits for the specified pod to reach its Ready condition, it
// encounters an error, or the provided context is canceled or expires.
//
// Deprecated: Use [KubeAPI] instead.
func WaitForPod(ctx context.Context, kc *kubernetes.Clientset, name, namespace string) error {
	return (&KubeAPI{client: kc}).WaitForPod(ctx, name, namespace)
}

// WaitForPodLogs waits until it can stream the logs of an arbitrary running
// pod in a given namespace, it encounters an error, or the provided context is
// canceled or expires. This is useful to test if k0s's konnectivity component
// is operational.
//
// Deprecated: Use [KubeAPI] instead.
func WaitForPodLogs(ctx context.Context, kc *kubernetes.Clientset, namespace string) error {
	return (&KubeAPI{client: kc}).WaitForPodLogs(ctx, namespace)
}

// WaitForLease waits for a specific Kubernetes lease to be acquired, it
// encounters an error, or the provided context is canceled or expires. It
// returns the holder identity of the lease on success.
//
// Deprecated: Use [KubeAPI] instead.
func WaitForLease(ctx context.Context, kc *kubernetes.Clientset, name string, namespace string) (string, error) {
	return (&KubeAPI{client: kc}).WaitForLease(ctx, name, namespace)
}

// RetryWatchErrors returns a callback function for handling errors during watch
// operations. It attempts to retry the watch after certain errors, providing
// resilience against intermittent errors.
func RetryWatchErrors(logf LogfFn) watch.ErrorCallback {
	return func(err error) (time.Duration, error) {
		if retryDelay, e := watch.IsRetryable(err); e == nil {
			logf("Encountered transient watch error, retrying in %s: %v", retryDelay, err)
			return retryDelay, nil
		}

		retryDelay := 1 * time.Second

		switch {
		case errors.Is(err, syscall.ECONNRESET):
			logf("Encountered connection reset while watching, retrying in %s: %v", retryDelay, err)
			return retryDelay, nil

		case errors.Is(err, syscall.ECONNREFUSED):
			logf("Encountered connection refused while watching, retrying in %s: %v", retryDelay, err)
			return retryDelay, nil

		case errors.Is(err, io.EOF):
			logf("Encountered EOF while watching, retrying in %s: %v", retryDelay, err)
			return retryDelay, nil
		}

		return 0, err
	}
}

// VerifySomeKubeSystemPods checks for the presence of some pods in the
// kube-system namespace.
//
// Deprecated: Use [KubeAPI] instead.
func VerifySomeKubeSystemPods(ctx context.Context, client kubernetes.Interface) error {
	return (&KubeAPI{client: client}).VerifySomeKubeSystemPods(ctx)
}

// Retrieves the LogfFn stored in context, falling back to use testing.T's Logf
// if the context has a *testing.T or logrus's Infof as a last resort.
func logfFrom(ctx context.Context) LogfFn {
	if logf := k0scontext.Value[LogfFn](ctx); logf != nil {
		return logf
	}
	if t := k0scontext.Value[*testing.T](ctx); t != nil {
		return t.Logf
	}
	return logrus.Infof
}
