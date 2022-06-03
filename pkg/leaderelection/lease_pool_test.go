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
package leaderelection

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	coordinationv1 "k8s.io/api/coordination/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestLeasePoolWatcherTriggersOnLeaseAcquisition(t *testing.T) {
	const identity = "test-node"

	fakeClient := fake.NewSimpleClientset()
	expectCreateNamespace(t, fakeClient)
	expectCreateLease(t, fakeClient, identity)

	pool, err := NewLeasePool(fakeClient, "test", WithIdentity(identity), WithNamespace("test"))
	require.NoError(t, err)

	var runner poolRunner
	runner.runPool(t, pool)

	leaseCtx := <-runner.leaseCtxChan
	assert.NoError(t, leaseCtx.Err(), "Lease context already cancelled")
	assert.Nil(t, runner.runErr.Load(), "Run already returned")

	t.Log("Acquired lease, cancelling pool context")
	runner.cancelPool()

	<-leaseCtx.Done()
	t.Log("Lease context cancelled")

	<-runner.runReturned
	t.Log("Run returned")

	runErr := runner.runErr.Load()
	if assert.NotNil(t, runErr, "Runner didn't return?") {
		assert.NoError(t, *runErr.(*error), "Run returned an error")
	}
}

func TestLeasePoolWatcherReacquiresLostLease(t *testing.T) {
	const identity = "test-node"

	fakeClient := fake.NewSimpleClientset()
	expectCreateNamespace(t, fakeClient)
	expectCreateLease(t, fakeClient, identity)

	redPool, err := NewLeasePool(fakeClient, "test", WithIdentity("red-node"), WithNamespace("test"))
	require.NoError(t, err)

	blackPool, err := NewLeasePool(fakeClient, "test", WithIdentity("black-node"), WithNamespace("test"))
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.TODO(), 10*time.Second)
	defer cancel()
	redCtx, cancelRed := context.WithCancel(ctx)
	defer cancelRed()
	blackCtx, cancelBlack := context.WithCancel(ctx)
	defer cancelBlack()

	type event = struct {
		name  string
		value interface{}
	}

	events := make(chan event)

	go func() {
		err := redPool.Run(redCtx, func(leaseCtx context.Context) {
			events <- event{"redLeads", leaseCtx}
		})
		events <- event{"redReturned", err}
	}()

	go func() {
		err := blackPool.Run(blackCtx, func(leaseCtx context.Context) {
			events <- event{"blackLeads", leaseCtx}
		})
		events <- event{"blackReturned", err}
	}()

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case event := <-events:
			t.Logf("Event: %s %#v", event.name, event.value)

		case <-ticker.C:
			t.Log("Tick")
		case <-ctx.Done():
			assert.FailNow(t, "Timeout")
		}

	}

	// var redRunner, blackRunner poolRunner

	// redRunner.runPool(redPool)
	// blackRunner.runPool(blackPool)

	// var currentLeader *LeasePool
	// var redWasLeader, blackWasLeader bool
	// for {
	// 	select {
	// 	case <-redRunner.leaseCtxChan:
	// 		assert.False(t, redWasLeader, "Red was already leading")
	// 		blackRunner.runErr

	// 		currentLeader
	// 		leadersSeen++
	// 		t.Log("Red pool acquired lease, cancelling its pool context")
	// 		redRunner.cancelPool()
	// 	case <-redRunner.runReturned:
	// 		t.Log("Red pool returned")
	// 		assert.

	// 	case <-events.LostLease:
	// 		fmt.Println("context cancelled and node 1 lease successfully lost")
	// 		receivedEvents = append(receivedEvents, "node1-lost")
	// 	case <-events2.AcquiredLease:
	// 		fmt.Println("node 2 lease acquired")
	// 		receivedEvents = append(receivedEvents, "node2-acquired")
	// 	default:
	// 		if len(receivedEvents) >= 3 {
	// 			break leaseEventLoop
	// 		}
	// 	}
	// }

	// blackRunner.runPool(blackPool)

	// events, cancel, err := redPool.Watch(WithOutputChannels(&LeaseEvents{
	// 	AcquiredLease: make(chan struct{}, 1),
	// 	LostLease:     make(chan struct{}, 1),
	// }))

	// assert.NoError(t, err)

	// events2, cancel2, err := blackPool.Watch(WithOutputChannels(&LeaseEvents{
	// 	AcquiredLease: make(chan struct{}, 1),
	// 	LostLease:     make(chan struct{}, 1),
	// }))
	// assert.NoError(t, err)
	// fmt.Println("started second lease holder")
	// defer cancel2()

	// var receivedEvents []string

	// leaseEventLoop:
	// 	for {
	// 		select {
	// 		case <-events.AcquiredLease:
	// 			fmt.Println("lease acquired, cancelling leaser")
	// 			cancel()
	// 			receivedEvents = append(receivedEvents, "node1-acquired")
	// 		case <-events.LostLease:
	// 			fmt.Println("context cancelled and node 1 lease successfully lost")
	// 			receivedEvents = append(receivedEvents, "node1-lost")
	// 		case <-events2.AcquiredLease:
	// 			fmt.Println("node 2 lease acquired")
	// 			receivedEvents = append(receivedEvents, "node2-acquired")
	// 		default:
	// 			if len(receivedEvents) >= 3 {
	// 				break leaseEventLoop
	// 			}
	// 		}
	// 	}

	// 	assert.Equal(t, "node1-acquired", receivedEvents[0])
	// 	assert.Equal(t, "node1-lost", receivedEvents[1])
	// 	assert.Equal(t, "node2-acquired", receivedEvents[2])
}

type poolRunner struct {
	poolCtx      context.Context
	cancelPool   context.CancelFunc
	leaseCtxChan chan context.Context
	runErr       atomic.Value
	runReturned  chan struct{}
}

func (r *poolRunner) getRunErr(t *testing.T) error {
	runErr := r.runErr.Load()
	require.NotNil(t, runErr, "Run didn't return yet")
	return *(runErr.(*error))
}

func (r *poolRunner) runPool(t *testing.T, pool *LeasePool) {
	r.poolCtx, r.cancelPool = context.WithCancel(context.TODO())
	r.leaseCtxChan = make(chan context.Context)
	r.runReturned = make(chan struct{})

	go func() {
		t.Logf("(%p) Pool runner started", r)
		err := pool.Run(r.poolCtx, func(leaseCtx context.Context) {
			t.Logf("(%p) Pool acquired lease", r)
			r.leaseCtxChan <- leaseCtx
			close(r.leaseCtxChan)
		})
		r.runErr.Store(&err)
		t.Logf("(%p) Pool returned: %v", r, err)
		close(r.runReturned)
	}()
}

func expectCreateNamespace(t *testing.T, fakeClient *fake.Clientset) {
	_, err := fakeClient.CoreV1().Namespaces().Create(context.TODO(), &v1.Namespace{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Namespace",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "test",
		},
		Spec: v1.NamespaceSpec{},
	}, metav1.CreateOptions{})
	require.NoError(t, err)
}

func expectCreateLease(t *testing.T, fakeClient *fake.Clientset, identity string) {
	_, err := fakeClient.CoordinationV1().Leases("test").Create(context.TODO(), &coordinationv1.Lease{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Lease",
			APIVersion: "coordination.k8s.io/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "test",
		},
		Spec: coordinationv1.LeaseSpec{
			HolderIdentity: &identity,
		},
	}, metav1.CreateOptions{})
	require.NoError(t, err)
}
