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
	"testing"
	"time"

	"github.com/k0sproject/k0s/internal/autopilot/testutil"
	"github.com/k0sproject/k0s/pkg/autopilot/constant"
	"github.com/k0sproject/k0s/pkg/leaderelection"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

// TestLeasesInitialPending ensures that when a lease watcher is created,
// the first event received is a 'pending' event.
func TestLeasesInitialPending(t *testing.T) {
	clientFactory := testutil.NewFakeClientFactory()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	logger := logrus.StandardLogger().WithField("app", "leases_test")

	leaseWatcher, err := NewLeaseWatcher(logger, clientFactory)
	assert.NoError(t, err)

	leaseEventStatusCh, errorCh := leaseWatcher.StartWatcher(ctx, constant.AutopilotNamespace, fmt.Sprintf("%s-lease", constant.AutopilotNamespace), t.Name())
	assert.NotNil(t, errorCh)
	assert.NotNil(t, leaseEventStatusCh)

	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()

	select {
	case <-timer.C:
		assert.Fail(t, "Timed out waiting for LeaseEventStatus")

	case leaseEventStatus, ok := <-leaseEventStatusCh:
		assert.True(t, ok)
		assert.NotEmpty(t, leaseEventStatus)
		assert.Equal(t, LeasePending, leaseEventStatus)
	}
}

// TestLeadershipWatcher runs through a table of tests that describe
// various lease acquired/lost scenarios
func TestLeadershipWatcher(t *testing.T) {
	var tests = []struct {
		name           string
		expectedEvents []LeaseEventStatus
		eventSource    func(acquiredLease, lostLease chan<- struct{})
	}{
		{
			"AcquiredThenLost",
			[]LeaseEventStatus{
				LeaseAcquired,
				LeasePending,
			},
			func(acquiredLease, lostLease chan<- struct{}) {
				sendEventAfter100ms(acquiredLease)
				sendEventAfter100ms(lostLease)
			},
		},
		{
			"LostThenAcquired",
			[]LeaseEventStatus{
				LeasePending,
				LeaseAcquired,
			},
			func(acquiredLease, lostLease chan<- struct{}) {
				sendEventAfter100ms(lostLease)
				sendEventAfter100ms(acquiredLease)
			},
		},
		{
			"AcquiredThenLostThenAcquired",
			[]LeaseEventStatus{
				LeaseAcquired,
				LeasePending,
			},
			func(acquiredLease, lostLease chan<- struct{}) {
				sendEventAfter100ms(acquiredLease)
				sendEventAfter100ms(lostLease)
				sendEventAfter100ms(acquiredLease)
			},
		},
		{
			"DoubleLostMakesNoSense",
			[]LeaseEventStatus{
				LeasePending,
			},
			func(acquiredLease, lostLease chan<- struct{}) {
				sendEventAfter100ms(lostLease)
			},
		},
		{
			"DoubleAcquireMakesNoSense",
			[]LeaseEventStatus{
				LeaseAcquired,
			},
			func(acquiredLease, lostLease chan<- struct{}) {
				sendEventAfter100ms(acquiredLease)
				sendEventAfter100ms(acquiredLease)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			leaseEventStatusCh := make(chan LeaseEventStatus, 100)

			acquiredLease, lostLease := make(chan struct{}), make(chan struct{})

			go func() {
				defer close(acquiredLease)
				defer close(lostLease)
				test.eventSource(acquiredLease, lostLease)
			}()

			leadershipWatcher(leaseEventStatusCh, &leaderelection.LeaseEvents{
				AcquiredLease: acquiredLease,
				LostLease:     lostLease,
			})

			close(leaseEventStatusCh)

			assert.Equal(t, test.expectedEvents, realizeLeaseEventStatus(leaseEventStatusCh))
		})
	}
}

func realizeLeaseEventStatus(ch chan LeaseEventStatus) []LeaseEventStatus {
	s := make([]LeaseEventStatus, 0)
	for ev := range ch {
		s = append(s, ev)
	}
	return s
}

func sendEventAfter100ms(out chan<- struct{}) {
	time.Sleep(100 * time.Millisecond)
	out <- struct{}{}
}
