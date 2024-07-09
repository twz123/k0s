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

package leaderelection

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	coordinationv1 "k8s.io/api/coordination/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	fakecoordinationv1 "k8s.io/client-go/kubernetes/typed/coordination/v1/fake"
	k8stesting "k8s.io/client-go/testing"
	"k8s.io/utils/ptr"
)

func TestLeasePoolTriggersLostLeaseWhenCancelled(t *testing.T) {
	const identity = "test-node"

	fakeClient := fake.NewSimpleClientset()

	pool, err := NewLeasePool(context.TODO(), fakeClient, "test", identity, WithNamespace("test"))
	require.NoError(t, err)

	errs := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.TODO())
	t.Cleanup(cancel)
	go func() {
		defer close(errs)
		errs <- pool.Run(ctx)
	}()

	isLeader, changed := pool.IsLeader().Peek()
	for !isLeader {
		select {
		case <-changed:
		case err := <-errs:
			require.Fail(t, "%v", err)
		}

		isLeader, changed = pool.IsLeader().Peek()
	}

	t.Log("Pool is leading, cancelling ...")
	cancel()

	select {
	case <-changed:
	case err := <-errs:
		require.Fail(t, "%v", err)
	}

	assert.False(t, pool.IsLeader().Get())
}

func TestLeasePoolWatcherReacquiresLostLease(t *testing.T) {
	const identity = "test-node"

	fakeClient := fake.NewSimpleClientset()

	givenLeaderElectorError := func() func(err error) {
		var updateErr atomic.Value
		fakeClient.CoordinationV1().(*fakecoordinationv1.FakeCoordinationV1).PrependReactor("update", "leases", func(action k8stesting.Action) (bool, runtime.Object, error) {
			if err := *updateErr.Load().(*error); err != nil {
				return true, nil, err
			}
			return false, nil, nil
		})

		return func(err error) {
			updateErr.Store(&err)
		}
	}()

	pool, err := NewLeasePool(context.TODO(), fakeClient, "test", identity,
		WithNamespace("test"),
		WithRetryPeriod(35*time.Millisecond),
		WithRenewDeadline(150*time.Millisecond),
	)
	require.NoError(t, err)

	givenLeaderElectorError(nil)
	errs := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.TODO())
	t.Cleanup(cancel)
	go func() {
		defer close(errs)
		errs <- pool.Run(ctx)
	}()

	for {
		isLeader, changed := pool.IsLeader().Peek()
		if isLeader {
			break
		}

		t.Log("Waiting for the pool to lead")

		select {
		case <-changed:
		case err := <-errs:
			require.Fail(t, "%v", err)
		}
	}

	t.Log("Pool is leading, disrupting leader election and waiting to loose the lease")
	givenLeaderElectorError(errors.New("leader election disrupted by test case"))

	for {
		isLeader, changed := pool.IsLeader().Peek()
		if !isLeader {
			break
		}

		t.Log("Waiting to loose leadership")

		select {
		case <-changed:
		case err := <-errs:
			require.Fail(t, "%v", err)
		}
	}

	t.Log("Lost leadership, restoring leader election and waiting to lead again")
	givenLeaderElectorError(nil)

	for {
		isLeader, changed := pool.IsLeader().Peek()
		if isLeader {
			break
		}

		t.Log("Waiting for the pool to lead again")

		select {
		case <-changed:
		case err := <-errs:
			require.Fail(t, "%v", err)
		}
	}

	t.Log("Pool is leading again, waiting fot it to close")

	cancel()
	assert.Nil(t, <-errs)
}

func TestSecondWatcherAcquiresReleasedLease(t *testing.T) {
	fakeClient := fake.NewSimpleClientset()

	pool1, err := NewLeasePool(context.TODO(), fakeClient, "test", "pool1",
		WithNamespace("test"),
		WithRetryPeriod(10*time.Millisecond),
	)
	require.NoError(t, err)

	pool2, err := NewLeasePool(context.TODO(), fakeClient, "test", "pool2",
		WithNamespace("test"),
		WithRetryPeriod(10*time.Millisecond),
	)
	require.NoError(t, err)

	// Pre-create the acquired lease for the first identity, so that there are
	// no races when acquiring the lease by the two competing pools.
	now := metav1.NewMicroTime(time.Now())
	_, err = fakeClient.CoordinationV1().Leases("test").Create(context.TODO(), &coordinationv1.Lease{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Lease",
			APIVersion: coordinationv1.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "test",
		},
		Spec: coordinationv1.LeaseSpec{
			HolderIdentity:       ptr.To("pool1"),
			AcquireTime:          &now,
			RenewTime:            &now,
			LeaseDurationSeconds: ptr.To(int32((1 * time.Hour).Seconds())), // block lease for a very long time
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)
	t.Log("Pre-created acquired lease for first identity")

	errs := make(chan error, 2)
	var wg sync.WaitGroup
	t.Cleanup(wg.Wait)

	ctx1, cancel1 := context.WithCancel(context.TODO())
	t.Cleanup(cancel1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		t.Log("Starting first lease pool")
		if err := pool1.Run(ctx1); err != nil {
			errs <- fmt.Errorf("pool1: %w", err)
		}
	}()

	ctx2, cancel2 := context.WithCancel(context.TODO())
	t.Cleanup(cancel2)
	wg.Add(1)
	go func() {
		defer wg.Done()
		t.Log("Starting second lease pool")
		if err := pool2.Run(ctx2); err != nil {
			errs <- fmt.Errorf("pool2: %w", err)
		}
	}()

	for {
		firstLeads, firstChanged := pool1.IsLeader().Peek()
		secondLeads, secondChanged := pool2.IsLeader().Peek()

		require.False(t, secondLeads)

		if firstLeads {
			t.Log("First pool leads")
			cancel1()
			break
		}

		select {
		case <-firstChanged:
		case <-secondChanged:
		case err := <-errs:
			require.Fail(t, "%v", err)
		}
	}

	for {
		firstLeads, firstChanged := pool1.IsLeader().Peek()
		secondLeads, secondChanged := pool2.IsLeader().Peek()

		if firstLeads {
			require.False(t, secondLeads)
			t.Log("First pool still leading")
		} else if secondLeads {
			require.False(t, firstLeads)
			t.Log("Second pool leads")
			break
		}

		select {
		case <-firstChanged:
		case <-secondChanged:
		case err := <-errs:
			require.Fail(t, "%v", err)
		}
	}
}
