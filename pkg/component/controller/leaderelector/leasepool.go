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

	mu     sync.Mutex
	cancel context.CancelCauseFunc
	done   <-chan struct{}

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

	if l.cancel != nil {
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

	ctx, cancel := context.WithCancelCause(context.Background())
	done := make(chan struct{})

	go func() {
		defer close(done)
		l.run(ctx)
	}()

	l.cancel, l.done = cancel, done

	return nil
}

func (l *LeasePool) run(ctx context.Context) {
	var wg sync.WaitGroup

	wg.Add(2)
	go func() { defer wg.Done(); l.client.Run(ctx, l.status.Set) }()
	go func() { defer wg.Done(); l.invokeCallbacks(ctx) }()
	wg.Wait()
}

func (l *LeasePool) YieldLease() {
	l.suspendFor(30 * time.Second)
}

func (l *LeasePool) suspendFor(suspend time.Duration) {
	ctx, cancelYield := context.WithCancelCause(context.Background())
	yieldDone := make(chan struct{})

	l.mu.Lock()
	cancel, done := l.cancel, l.done
	if cancel == nil {
		l.mu.Unlock()
		cancelYield(nil) // The context is not used, cancel it just to silence linters.
		return
	}
	l.cancel, l.done = cancelYield, yieldDone
	l.mu.Unlock()

	l.log.Info("Suspending for ", suspend)
	delay := time.After(suspend)

	go func() {
		defer close(yieldDone)
		cancel(errors.New("lease pool is yielding"))
		<-done

		select {
		case <-delay:
			l.log.Info("Resuming operations")
			l.run(ctx)

		case <-ctx.Done():
		}
	}()
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
	cancel, done := l.cancel, l.done
	l.cancel = nil
	l.mu.Unlock()

	if cancel != nil {
		cancel(errors.New("lease pool is stopping"))
	}
	if done != nil {
		<-done
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
