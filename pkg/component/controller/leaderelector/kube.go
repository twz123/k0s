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

package leaderelector

import (
	"context"
	"sync/atomic"

	"github.com/k0sproject/k0s/pkg/component"
	kubeutil "github.com/k0sproject/k0s/pkg/kubernetes"

	"github.com/k0sproject/k0s/pkg/leaderelection"
	"github.com/sirupsen/logrus"
	"k8s.io/client-go/kubernetes"
)

type LeasePoolLeaderElector struct {
	log logrus.FieldLogger

	stopCh       chan struct{}
	leaderStatus atomic.Value
	client       kubernetes.Interface
	leaseCancel  context.CancelFunc

	acquiredLeaseCallbacks []func()
	lostLeaseCallbacks     []func()
}

var _ LeaderElector = (*LeasePoolLeaderElector)(nil)
var _ component.Component = (*LeasePoolLeaderElector)(nil)

// NewLeasePoolLeaderElector creates new leader elector using a Kubernetes lease pool.
func NewLeasePoolLeaderElector(kubeClientFactory kubeutil.ClientFactoryInterface) *LeasePoolLeaderElector {
	d := atomic.Value{}
	d.Store(false)
	return &LeasePoolLeaderElector{
		log: logrus.WithFields(logrus.Fields{"component": "endpointreconciler"}),

		stopCh:       make(chan struct{}),
		client:       client,
		leaderStatus: d,
	}
}

func (*LeasePoolLeaderElector) Init(context.Context) error { return nil }
func (*LeasePoolLeaderElector) Healthy() error             { return nil }

func (l *LeasePoolLeaderElector) Run(ctx context.Context) error {

	leasePool, err := leaderelection.NewLeasePool(client, "k0s-endpoint-reconciler",
		leaderelection.WithLogger(l.log),
		leaderelection.WithContext(ctx))
	if err != nil {
		return err
	}

	events, cancel, err := leasePool.Watch()
	if err != nil {
		return err
	}
	l.leaseCancel = cancel

	go func() {
		for {
			select {
			case <-events.AcquiredLease:
				l.log.Info("acquired leader lease")
				l.leaderStatus.Store(true)
				runCallbacks(l.acquiredLeaseCallbacks)
			case <-events.LostLease:
				l.log.Info("lost leader lease")
				l.leaderStatus.Store(false)
				runCallbacks(l.lostLeaseCallbacks)
			}
		}
	}()
	return nil
}

func runCallbacks(callbacks []func()) {
	for _, fn := range callbacks {
		if fn != nil {
			fn()
		}
	}
}

func (l *LeasePoolLeaderElector) AddAcquiredLeaseCallback(fn func()) {
	l.acquiredLeaseCallbacks = append(l.acquiredLeaseCallbacks, fn)
}

func (l *LeasePoolLeaderElector) AddLostLeaseCallback(fn func()) {
	l.lostLeaseCallbacks = append(l.lostLeaseCallbacks, fn)
}

func (l *LeasePoolLeaderElector) Stop() error {
	if l.leaseCancel != nil {
		l.leaseCancel()
	}
	return nil
}

func (l *LeasePoolLeaderElector) IsLeader() bool {
	return l.leaderStatus.Load().(bool)
}
