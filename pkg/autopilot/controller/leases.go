// Copyright 2021 k0s authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package controller

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/k0sproject/k0s/internal/sync/value"
	"github.com/k0sproject/k0s/pkg/autopilot/client"
	"github.com/k0sproject/k0s/pkg/leaderelection"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/wait"
	clientset "k8s.io/client-go/kubernetes"
)

type LeaseEventStatus bool

const (
	LeasePending  LeaseEventStatus = false
	LeaseAcquired LeaseEventStatus = true
)

type LeaseStatus struct {
	Status LeaseEventStatus
	Err    error
}

// LeaseWatcher outlines the lease operations for the autopilot configuration.
type LeaseWatcher interface {
	StartWatcher(ctx context.Context, namespace string, name, identity string) value.Peeker[LeaseStatus]
}

// NewLeaseWatcher creates a new `LeaseWatcher` using the appropriate clientset
func NewLeaseWatcher(logEntry *logrus.Entry, cf client.FactoryInterface) (LeaseWatcher, error) {
	client, err := cf.GetClient()
	if err != nil {
		return nil, err
	}

	return &leaseWatcher{
		log:    logEntry,
		client: client,
	}, nil
}

type leaseWatcher struct {
	log    *logrus.Entry
	client clientset.Interface
}

var _ LeaseWatcher = (*leaseWatcher)(nil)

func (lw *leaseWatcher) StartWatcher(ctx context.Context, namespace string, name, identity string) value.Peeker[LeaseStatus] {
	var status value.Latest[LeaseStatus]

	go func() {
		wait.UntilWithContext(ctx, func(ctx context.Context) {
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()

			leasePoolOpts := []leaderelection.LeaseOpt{
				leaderelection.WithContext(ctx),
				leaderelection.WithNamespace(namespace),
			}

			leasePool, err := leaderelection.NewLeasePool(ctx, lw.client, name, identity, leasePoolOpts...)
			if err != nil {
				status.Set(LeaseStatus{Err: fmt.Errorf("failed to create lease pool: %w", err)})
				cancel()
				return
			}

			errs := make(chan error, 1)
			go func() {
				defer close(errs)
				if err := leasePool.Run(ctx); err != nil {
					errs <- fmt.Errorf("failed to run lease pool: %w", err)
				}
			}()

			lastStatus := status.Get()
			for {
				isLeader, changed := leasePool.IsLeader().Peek()
				currentStatus := LeaseStatus{Status: LeaseEventStatus(isLeader)}
				if lastStatus != currentStatus {
					status.Set(currentStatus)
					lastStatus = currentStatus
				}

				select {
				case <-changed:
				case err := <-errs:
					cancel()
					status.Set(LeaseStatus{Err: err})
					return
				}
			}
		}, 3*time.Second)
	}()

	return &status
}

// leadershipWatcher watches events as they arrive, and pushes them out to the provided
// channel.
//
// There is a limitation in the `leaderelection` package in `client-go` that prevents
// leadership from being re-obtained if the time disconnected from the API server exceeds
// the 'retry-deadline'.
//
// To circumvent this problem, we take note when we have become a leader, and if we lose
// leadership at any point afterwards, this watcher goroutine will exit.
func leadershipWatcher(ctx context.Context, leaseEventStatusCh chan<- LeaseEventStatus, events *leaderelection.LeaseEvents) *sync.WaitGroup {
	wg := &sync.WaitGroup{}
	wg.Add(1)

	go func(ctx context.Context) {
		defer wg.Done()
		previouslyHeldLeadership := false
		var lastLeaseEventStatus LeaseEventStatus

		for {
			select {
			case _, ok := <-events.AcquiredLease:
				if !ok {
					return
				}

				if lastLeaseEventStatus != LeaseAcquired {
					lastLeaseEventStatus = LeaseAcquired
					leaseEventStatusCh <- lastLeaseEventStatus

					previouslyHeldLeadership = true
				}

			case _, ok := <-events.LostLease:
				if !ok {
					return
				}

				if lastLeaseEventStatus != LeasePending {
					lastLeaseEventStatus = LeasePending
					leaseEventStatusCh <- lastLeaseEventStatus

					if previouslyHeldLeadership {
						return
					}
				}

			case <-ctx.Done():
				return
			}
		}
	}(ctx)

	return wg
}
