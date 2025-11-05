// SPDX-FileCopyrightText: 2022 k0s authors
// SPDX-License-Identifier: Apache-2.0

package leaderelector

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/k0sproject/k0s/internal/sync/value"
	"github.com/k0sproject/k0s/pkg/component/manager"
	kubeutil "github.com/k0sproject/k0s/pkg/kubernetes"
	"github.com/k0sproject/k0s/pkg/leaderelection"

	corev1 "k8s.io/api/core/v1"

	"github.com/sirupsen/logrus"
)

type LeasePool struct {
	log *logrus.Entry

	invocationID      string
	status            value.Latest[leaderelection.Status]
	kubeClientFactory kubeutil.ClientFactoryInterface
	name              string
	client            leaseClient
	suspendUntil      value.Latest[time.Time]

	mu   sync.Mutex
	stop func()

	acquiredLeaseCallbacks []func()
	lostLeaseCallbacks     []func()
}

type leaseClient interface {
	Run(ctx context.Context, changed func(leaderelection.Status))
}

var (
	_ Interface         = (*LeasePool)(nil)
	_ manager.Component = (*LeasePool)(nil)
)

// NewLeasePool creates a new leader elector using a Kubernetes lease pool.
func NewLeasePool(invocationID string, kubeClientFactory kubeutil.ClientFactoryInterface, name string) *LeasePool {
	return &LeasePool{
		invocationID:      invocationID,
		kubeClientFactory: kubeClientFactory,
		log:               logrus.WithFields(logrus.Fields{"component": "poolleaderelector"}),
		name:              name,
	}
}

func (l *LeasePool) Init(context.Context) error {
	return nil
}

func (l *LeasePool) Start(context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.stop != nil {
		return nil
	}

	if l.client == nil {
		kubeClient, err := l.kubeClientFactory.GetClient()
		if err != nil {
			return fmt.Errorf("can't create kubernetes rest client for lease pool: %w", err)
		}

		l.client, err = leaderelection.NewClient(&leaderelection.LeaseConfig{
			Namespace: corev1.NamespaceNodeLease,
			Name:      l.name,
			Identity:  l.invocationID,
			Client:    kubeClient.CoordinationV1(),
		})
		if err != nil {
			return err
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		defer close(done)
		for ctx.Err() == nil {
			l.runClient(ctx)
		}
	}()

	l.stop = func() { cancel(); <-done }

	return nil
}

func (l *LeasePool) runClient(ctx context.Context) {
	suspendUntilChanged := l.waitForResume(ctx)
	if suspendUntilChanged == nil {
		return
	}

	runCtx, cancelRun := context.WithCancelCause(context.Background())

	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); l.client.Run(runCtx, l.status.Set) }()
	go func() { defer wg.Done(); l.invokeCallbacks(runCtx) }()

	select {
	case <-ctx.Done():
		cancelRun(errors.New("lease pool is stopping"))
		wg.Wait()
		return

	case <-suspendUntilChanged:
		l.log.Debug("Yielding")
		cancelRun(errors.New("lease pool is yielding"))
		wg.Wait()
	}
}

func (l *LeasePool) waitForResume(ctx context.Context) <-chan struct{} {
	suspendUntil, suspendUntilChanged := l.suspendUntil.Peek()

	d := time.Until(suspendUntil)
	if d <= 0 {
		return suspendUntilChanged
	}
	timer := time.NewTimer(d)
	defer timer.Stop()

	l.log.Info("Suspending until ", suspendUntil)

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-suspendUntilChanged:
			suspendUntil, suspendUntilChanged = l.suspendUntil.Peek()
			timer.Reset(time.Until(suspendUntil))
			l.log.Info("Suspending until ", suspendUntil)
		case <-timer.C:
			return suspendUntilChanged
		}
	}
}

func (l *LeasePool) YieldLease() {
	l.suspendFor(30 * time.Second)
}

func (l *LeasePool) suspendFor(duration time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.stop != nil {
		l.suspendUntil.Set(time.Now().Add(duration))
	}
}

func (l *LeasePool) invokeCallbacks(ctx context.Context) {
	leaderelection.RunLeaderTasks(ctx, l.status.Peek, func(leaderCtx context.Context) {
		l.log.Info("acquired leader lease")
		runCallbacks(l.acquiredLeaseCallbacks)
		<-leaderCtx.Done()
		l.log.Infof("lost leader lease (%v)", context.Cause(ctx))
		runCallbacks(l.lostLeaseCallbacks)
	})
}

func runCallbacks(callbacks []func()) {
	for _, fn := range callbacks {
		if fn != nil {
			fn()
		}
	}
}

// Deprecated: Use [LeasePool.CurrentStatus] instead.
func (l *LeasePool) AddAcquiredLeaseCallback(fn func()) {
	l.acquiredLeaseCallbacks = append(l.acquiredLeaseCallbacks, fn)
}

// Deprecated: Use [LeasePool.CurrentStatus] instead.
func (l *LeasePool) AddLostLeaseCallback(fn func()) {
	l.lostLeaseCallbacks = append(l.lostLeaseCallbacks, fn)
}

func (l *LeasePool) Stop() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.stop != nil {
		l.stop()
		l.stop = nil
		l.suspendUntil.Set(time.Time{})
	}
	return nil
}

// CurrentStatus is this lease pool's [leaderelection.StatusFunc].
func (l *LeasePool) CurrentStatus() (leaderelection.Status, <-chan struct{}) {
	return l.status.Peek()
}

// Deprecated: Use [LeasePool.CurrentStatus] instead.
func (l *LeasePool) IsLeader() bool {
	status, _ := l.CurrentStatus()
	return status == leaderelection.StatusLeading
}
