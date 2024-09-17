/*
Copyright 2024 k0s authors

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

package lifecycle_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/k0sproject/k0s/internal/lifecycle"
	"github.com/k0sproject/k0s/internal/testutil"
	"github.com/stretchr/testify/assert"
)

func TestGroup_Shutdown(t *testing.T) {
	var g lifecycle.Group
	var stopped atomic.Uint32

	ones := lifecycle.Go(&g, func(ctx context.Context, slot *lifecycle.Slot) (int, chan<- struct{}, <-chan struct{}, error) {
		t.Log("Ones slot:", slot)
		stop, done := make(chan struct{}), make(chan struct{})
		go func() {
			defer close(done)
			<-stop
			assert.True(t, stopped.CompareAndSwap(1, 2), "ones not stopped after tens")
		}()

		return 2, stop, done, nil
	})
	t.Log("Ones ref:", ones)

	tens := lifecycle.Go(&g, func(ctx context.Context, slot *lifecycle.Slot) (int, chan<- struct{}, <-chan struct{}, error) {
		t.Log("Tens slot:", slot)
		ones, err := lifecycle.Get(ctx, slot, ones)
		if err != nil {
			return 0, nil, nil, err
		}

		stop, done := make(chan struct{}), make(chan struct{})
		go func() {
			defer close(done)
			<-stop
			assert.True(t, stopped.CompareAndSwap(0, 1), "tens not stopped before ones")
		}()

		return 40 + ones, stop, done, nil
	})
	t.Log("Tens:", tens)

	g.Do(context.TODO(), func(ctx context.Context, s *lifecycle.Slot) {
		tens, err := lifecycle.Get(ctx, s, tens)
		if assert.NoError(t, err) {
			assert.Equal(t, 42, tens)
		}
	})

	// Test brute force shutdown
	var shutdownGoroutines sync.WaitGroup
	shutdown := make(chan struct{})
	for i := 0; i < 100; i++ {
		shutdownGoroutines.Add(1)
		go func() {
			defer shutdownGoroutines.Done()
			<-shutdown
			g.Shutdown(context.TODO())
		}()
	}

	close(shutdown)
	shutdownGoroutines.Wait()
	assert.Equal(t, uint32(2), stopped.Load(), "Not all goroutines have been stopped in the right order")
}

func TestGet_RejectsBogusStuff(t *testing.T) {
	var g lifecycle.Group
	var zeroSlot lifecycle.Slot
	var zeroRef lifecycle.Ref[any]

	_, err := lifecycle.Get(context.TODO(), &zeroSlot, &zeroRef)
	assert.ErrorContains(t, err, "invalid")

	ref := lifecycle.Go(&g, func(ctx context.Context, slot *lifecycle.Slot) (any, chan<- struct{}, <-chan struct{}, error) {
		return nil, nil, nil, nil
	})

	_, err = lifecycle.Get(context.TODO(), &zeroSlot, ref)
	assert.ErrorContains(t, err, "invalid slot")

	var disposedSlot *lifecycle.Slot
	g.Do(context.TODO(), func(ctx context.Context, slot *lifecycle.Slot) {
		disposedSlot = slot
		_, err := lifecycle.Get(ctx, slot, &zeroRef)
		assert.ErrorContains(t, err, "invalid ref")
	})

	_, err = lifecycle.Get(context.TODO(), disposedSlot, ref)
	assert.ErrorContains(t, err, "invalid slot")

	var other lifecycle.Group
	other.Do(context.TODO(), func(ctx context.Context, slot *lifecycle.Slot) {
		_, err := lifecycle.Get(ctx, slot, ref)
		assert.ErrorContains(t, err, "slot and ref incompatible")
	})
}

func TestGet_ContextCancellation(t *testing.T) {
	var g lifecycle.Group

	testCtx, cancelTest := context.WithCancelCause(context.TODO())

	getDone := make(chan struct{})
	goroutineDone := make(chan struct{})
	ref := lifecycle.Go(&g, func(ctx context.Context, slot *lifecycle.Slot) (any, chan<- struct{}, <-chan struct{}, error) {
		defer close(goroutineDone)
		<-testCtx.Done()

		select {
		case <-ctx.Done():
			assert.Fail(t, "The goroutine's context is not independent of the outer context")
		default:
		}

		<-getDone
		return nil, nil, nil, nil
	})

	g.Do(testCtx, func(ctx context.Context, slot *lifecycle.Slot) {
		defer close(getDone)
		time.AfterFunc(time.Microsecond, func() { cancelTest(assert.AnError) })
		_, err := lifecycle.Get(ctx, slot, ref)
		assert.ErrorIs(t, err, assert.AnError)
	})

	<-goroutineDone
}

func TestGet_SelfDependency(t *testing.T) {
	var g lifecycle.Group
	provide := make(chan struct{})

	var self *lifecycle.Ref[any]
	self = lifecycle.Go(&g, func(ctx context.Context, slot *lifecycle.Slot) (any, chan<- struct{}, <-chan struct{}, error) {
		<-provide
		if _, err := lifecycle.Get(ctx, slot, self); err != nil {
			return nil, nil, nil, fmt.Errorf("self: %w", err)
		}
		return nil, nil, nil, nil
	})

	close(provide)

	g.Do(context.TODO(), func(ctx context.Context, slot *lifecycle.Slot) {
		_, err := lifecycle.Get(ctx, slot, self)
		assert.ErrorContains(t, err, "self: circular dependency")
		assert.ErrorIs(t, err, lifecycle.ErrCircular)
	})
}

func TestGet_LoopDetection(t *testing.T) {
	order := [...]int{0, 1, 2, 3, 4, 5}
	testutil.Permute(order[:], func() bool {
		provide := make(chan struct{})
		seq := make([]chan struct{}, len(order))
		for i := range seq {
			seq[i] = make(chan struct{})
		}

		var g lifecycle.Group

		circle := make([]*lifecycle.Ref[any], len(order)-1)
		for i := range circle {
			order, i, j := order[i], i, (i+1)%len(circle)
			circle[i] = lifecycle.Go(&g, func(ctx context.Context, slot *lifecycle.Slot) (any, chan<- struct{}, <-chan struct{}, error) {
				<-provide
				for i := 0; i < order; i++ {
					<-seq[i]
				}
				time.AfterFunc(10*time.Microsecond, func() { close(seq[order]) })
				if _, err := lifecycle.Get(ctx, slot, circle[j]); err != nil {
					return nil, nil, nil, fmt.Errorf("(order %d) %d awaits %d: %w", order, i, j, err)
				}
				return nil, nil, nil, nil
			})
		}

		close(provide)

		g.Do(context.TODO(), func(ctx context.Context, slot *lifecycle.Slot) {
			for i := 0; i < order[len(order)-1]; i++ {
				<-seq[i]
			}
			close(seq[order[len(order)-1]])

			for i := range circle {
				order, i, j := order[i], i, (i+1)%len(circle)
				_, err := lifecycle.Get(ctx, slot, circle[i])
				assert.ErrorContains(t, err, fmt.Sprintf("(order %d) %d awaits %d: ", order, i, j))
				assert.ErrorIs(t, err, lifecycle.ErrCircular)
			}
		})

		return !t.Failed()
	})
}
