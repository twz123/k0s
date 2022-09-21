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
	"errors"
	"fmt"
	"net"
	"reflect"
	"sort"
	"sync/atomic"
	"time"

	"github.com/k0sproject/k0s/pkg/apis/k0s.k0sproject.io/v1beta1"
	"github.com/k0sproject/k0s/pkg/component"
	kubeutil "github.com/k0sproject/k0s/pkg/kubernetes"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/sirupsen/logrus"
)

// Dummy checks so we catch easily if we miss some interface implementation
var _ component.Component = (*APIEndpointReconciler)(nil)

// APIEndpointReconciler is the component to reconcile in-cluster API address endpoint based from externalName
type APIEndpointReconciler struct {
	log logrus.FieldLogger

	api               v1beta1.APISpec
	leaderElector     LeaderElector
	kubeClientFactory kubeutil.ClientFactoryInterface

	stop atomic.Pointer[func()]
}

// NewEndpointReconciler creates new endpoint reconciler
func NewEndpointReconciler(api v1beta1.APISpec, leaderElector LeaderElector, kubeClientFactory kubeutil.ClientFactoryInterface) *APIEndpointReconciler {
	return &APIEndpointReconciler{
		log: logrus.WithField("component", "APIEndpointReconciler"),

		api:               api,
		leaderElector:     leaderElector,
		kubeClientFactory: kubeClientFactory,
	}
}

// Init implements [component.Component].
func (a *APIEndpointReconciler) Init(context.Context) error { return nil }

// Run runs the main loop for reconciling the externalAddress.
func (a *APIEndpointReconciler) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})

	stop := func() {
		cancel()
		<-done
	}

	if !a.stop.CompareAndSwap(nil, &stop) {
		cancel()
		return errors.New("already started")
	}

	go func() {
		defer close(done)
		_ = wait.PollImmediateUntilWithContext(ctx, 10*time.Second, func(ctx context.Context) (done bool, err error) {
			if err := a.reconcileEndpoints(ctx); err != nil && (ctx.Err() == nil || !errors.Is(err, ctx.Err())) {
				a.log.WithError(err).Error("External API address reconciliation failed")
			}
			return false, nil
		})
		a.log.Info("Reconciliation stopped")
	}()

	return nil
}

// Stop stops the reconciler
func (a *APIEndpointReconciler) Stop() error {
	stop := a.stop.Load()
	if stop == nil {
		dummy := func() {}
		if a.stop.CompareAndSwap(nil, &dummy) {
			return nil
		}
	}

	(*stop)()
	return nil
}

func (a *APIEndpointReconciler) reconcileEndpoints(ctx context.Context) error {
	if !a.leaderElector.IsLeader() {
		a.log.Debug("Not the leader, not reconciling API endpoints")
		return nil
	}

	ips, err := net.LookupIP(a.api.ExternalAddress)
	if err != nil {
		return fmt.Errorf("failed to resolve external address %q: %w", a.api.ExternalAddress, err)
	}
	// Sort the addresses so we can more easily tell if we need to update the endpoints or not
	ipStrings := make([]string, len(ips))
	for i, ip := range ips {
		ipStrings[i] = ip.String()
	}
	sort.Strings(ipStrings)

	c, err := a.kubeClientFactory.GetClient()
	if err != nil {
		return err
	}

	epClient := c.CoreV1().Endpoints("default")

	ep, err := epClient.Get(ctx, "kubernetes", metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			err := a.createEndpoint(ctx, ipStrings)
			return err
		}

		return err
	}

	if len(ep.Subsets) == 0 || needsUpdate(ipStrings, ep) {
		ep.Subsets = []corev1.EndpointSubset{{
			Addresses: stringsToEndpointAddresses(ipStrings),
			Ports: []corev1.EndpointPort{{
				Name:     "https",
				Protocol: corev1.ProtocolTCP,
				Port:     int32(a.api.Port),
			}},
		}}

		_, err := epClient.Update(ctx, ep, metav1.UpdateOptions{})
		if err != nil {
			return err
		}
	}

	return nil
}

func (a *APIEndpointReconciler) createEndpoint(ctx context.Context, addresses []string) error {
	ep := &corev1.Endpoints{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Endpoints",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "kubernetes",
		},
		Subsets: []corev1.EndpointSubset{{
			Addresses: stringsToEndpointAddresses(addresses),
			Ports: []corev1.EndpointPort{{
				Name:     "https",
				Protocol: corev1.ProtocolTCP,
				Port:     int32(a.api.Port),
			}},
		}},
	}

	c, err := a.kubeClientFactory.GetClient()
	if err != nil {
		return err
	}

	_, err = c.CoreV1().Endpoints("default").Create(ctx, ep, metav1.CreateOptions{})
	if err != nil {
		return err
	}

	return nil
}

func needsUpdate(newAddresses []string, ep *corev1.Endpoints) bool {
	currentAddresses := endpointAddressesToStrings(ep.Subsets[0].Addresses)
	sort.Strings(currentAddresses)
	return !reflect.DeepEqual(currentAddresses, newAddresses)
}

func endpointAddressesToStrings(eps []corev1.EndpointAddress) []string {
	a := make([]string, len(eps))

	for i, e := range eps {
		a[i] = e.IP
	}

	return a
}

func stringsToEndpointAddresses(addresses []string) []corev1.EndpointAddress {
	eps := make([]corev1.EndpointAddress, len(addresses))

	for i, a := range addresses {
		eps[i] = corev1.EndpointAddress{
			IP: a,
		}
	}

	return eps
}
