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

package controller

import (
	"context"
	"fmt"
	"maps"
	"strconv"
	"sync"
	"time"

	"github.com/k0sproject/k0s/pkg/apis/k0s/v1beta1"
	"github.com/k0sproject/k0s/pkg/component/manager"
	kubeutil "github.com/k0sproject/k0s/pkg/kubernetes"
	"github.com/k0sproject/k0s/pkg/leaderelection"

	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	apitypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	clientcoordinationv1 "k8s.io/client-go/kubernetes/typed/coordination/v1"

	"github.com/sirupsen/logrus"
)

const konnectivityLabelName = "k0s.k0sproject.io/konnectivity-proxy-server"

// K0sControllersLeaseCounter implements a component that manages a lease per controller.
// The per-controller leases are used to determine the amount of currently running controllers
type K0sControllersLeaseCounter struct {
	NodeName            apitypes.NodeName
	InvocationID        string
	ClusterConfig       *v1beta1.ClusterConfig
	KubeClientFactory   kubeutil.ClientFactoryInterface
	KonnectivityEnabled bool

	// Deprecated: remove in k0s 1.34+
	UpdateControllerCount func(uint)

	log  logrus.FieldLogger
	stop func()
}

var _ manager.Component = (*K0sControllersLeaseCounter)(nil)

// Init initializes the component needs
func (l *K0sControllersLeaseCounter) Init(_ context.Context) error {
	l.log = logrus.WithField("component", "controllerlease")
	return nil
}

// Run runs the leader elector to keep the lease object up-to-date.
func (l *K0sControllersLeaseCounter) Start(context.Context) error {
	kubeClient, err := l.KubeClientFactory.GetClient()
	if err != nil {
		return fmt.Errorf("can't create kubernetes rest client for lease pool: %w", err)
	}

	// Use the node name to make the lease names be clear to which controller they belong to
	leaseName := "k0s-ctrl-" + string(l.NodeName)

	client, err := leaderelection.NewClient(&leaderelection.LeaseConfig{
		Namespace: corev1.NamespaceNodeLease,
		Name:      leaseName,
		Identity:  l.InvocationID,
		Client: &controllerLeasesGetter{
			LeasesGetter: kubeClient.CoordinationV1(),
			modifyLease: func(lease *coordinationv1.Lease) *coordinationv1.Lease {
				return addLeaseLabel(lease, konnectivityLabelName, strconv.FormatBool(l.KonnectivityEnabled))
			},
		},
	})
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup

	wg.Add(2)
	go func() { defer wg.Done(); l.runLeaderElection(ctx, client) }()
	go func() { defer wg.Done(); l.runLeaseCounter(ctx, kubeClient) }()
	l.stop = func() { cancel(); wg.Wait() }

	return nil
}

// Stop stops the component
func (l *K0sControllersLeaseCounter) Stop() error {
	if l.stop != nil {
		l.stop()
	}
	return nil
}

func (l *K0sControllersLeaseCounter) runLeaderElection(ctx context.Context, client *leaderelection.Client) {
	l.log.Info("Trying to acquire the controller lease")
	client.Run(ctx, func(status leaderelection.Status) {
		if status == leaderelection.StatusLeading {
			l.log.Info("Holding the controller lease")
		} else {
			l.log.Error("Lost the controller lease")
		}
	})
}

// Updates the number of active controller leases every 10 secs.
//
// Deprecated: remove in k0s 1.34+
func (l *K0sControllersLeaseCounter) runLeaseCounter(ctx context.Context, clients kubernetes.Interface) {
	l.log.Debug("Starting controller lease counter every 10 secs")

	wait.UntilWithContext(ctx, func(ctx context.Context) {
		l.log.Debug("Counting active controller leases")
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		count, err := kubeutil.CountActiveControllerLeases(ctx, clients)
		if err != nil {
			l.log.WithError(err).Error("Failed to count controller lease holders")
			return
		}

		l.UpdateControllerCount(count)
	}, 10*time.Second)
}

func addLeaseLabel(lease *coordinationv1.Lease, name, value string) *coordinationv1.Lease {
	if lease == nil {
		return nil
	}

	if lease.Labels != nil && lease.Labels[name] == value {
		return lease
	}

	labels := make(map[string]string, len(lease.Labels)+1)
	maps.Copy(labels, lease.Labels)
	labels[name] = value

	lease = lease.DeepCopy()
	lease.Labels = labels
	return lease
}

type controllerLeasesGetter struct {
	clientcoordinationv1.LeasesGetter
	modifyLease func(*coordinationv1.Lease) *coordinationv1.Lease
}

func (g *controllerLeasesGetter) Leases(namespace string) clientcoordinationv1.LeaseInterface {
	return &controllerLeaseClient{
		g.LeasesGetter.Leases(namespace),
		g.modifyLease,
	}
}

type controllerLeaseClient struct {
	clientcoordinationv1.LeaseInterface
	modifyLease func(*coordinationv1.Lease) *coordinationv1.Lease
}

func (c *controllerLeaseClient) Create(ctx context.Context, lease *coordinationv1.Lease, opts metav1.CreateOptions) (*coordinationv1.Lease, error) {
	return c.LeaseInterface.Create(ctx, c.modifyLease(lease), opts)
}

func (c *controllerLeaseClient) Update(ctx context.Context, lease *coordinationv1.Lease, opts metav1.UpdateOptions) (*coordinationv1.Lease, error) {
	return c.LeaseInterface.Update(ctx, c.modifyLease(lease), opts)
}
