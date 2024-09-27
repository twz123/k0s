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
	"github.com/stretchr/testify/require"
)

func TestGroup_Shutdown(t *testing.T) {
	var g lifecycle.Group
	var stopped atomic.Uint32

	ones := lifecycle.GoFunc(&g, func(ctx context.Context) (*lifecycle.Task[int], error) {
		stop, done := make(chan struct{}), make(chan struct{})
		go func() {
			defer close(done)
			<-stop
			assert.True(t, stopped.CompareAndSwap(1, 2), "ones not stopped after tens")
		}()

		return lifecycle.TaskOf(stop, done, 2)
	})

	tens := lifecycle.GoFunc(&g, func(ctx context.Context) (*lifecycle.Task[int], error) {
		ones, err := ones.Require(ctx)
		if err != nil {
			return nil, err
		}

		stop, done := make(chan struct{}), make(chan struct{})
		go func() {
			defer close(done)
			<-stop
			assert.True(t, stopped.CompareAndSwap(0, 1), "tens not stopped before ones")
		}()

		return lifecycle.TaskOf(stop, done, 40+ones)
	})

	g.Do(context.TODO(), func(ctx context.Context) {
		tens, err := tens.Require(ctx)
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

	t.Run("no_start_after_shutdown", func(t *testing.T) {
		lifecycle.GoFunc(&g, func(ctx context.Context) (*lifecycle.Unit, error) {
			require.Fail(t, "u no call me once")
			return nil, nil
		})
		lifecycle.GoFunc(&g, func(ctx context.Context) (*lifecycle.Unit, error) {
			require.Fail(t, "u no call me twice")
			return nil, nil
		})
	})
}

func TestGroup_Shutdown_CancelResume(t *testing.T) {
	var g lifecycle.Group

	ctx, cancel := context.WithCancelCause(context.TODO())
	t.Cleanup(func() { cancel(nil) })

	proceed := make(chan struct{})
	done := make(chan struct{})
	first := lifecycle.GoFunc(&g, func(ctx context.Context) (*lifecycle.Task[int], error) {
		<-ctx.Done()
		cancel(assert.AnError)
		close(proceed)
		return lifecycle.TaskOf(nil, done, 0)
	})
	lifecycle.GoFunc(&g, func(ctx context.Context) (*lifecycle.Task[int], error) {
		first.Require(ctx)
		<-proceed
		close(done)
		return nil, nil
	})

	assert.Same(t, assert.AnError, g.Shutdown(ctx))
	assert.NoError(t, g.Shutdown(context.TODO()))
}

func TestRef_String(t *testing.T) {
	var g lifecycle.Group

	proceed := make(chan struct{})
	okRef := lifecycle.GoFunc(&g, func(ctx context.Context) (*lifecycle.Task[int], error) {
		<-proceed
		return lifecycle.TaskOf(nil, nil, 42)
	})
	errRef := lifecycle.GoFunc(&g, func(ctx context.Context) (*lifecycle.Task[int], error) {
		<-proceed
		return nil, fmt.Errorf("this didn't work")
	})

	assert.Regexp(t, `^\*lifecycle.Ref\[int\]\(0x[0-9a-f]+ starting\)$`, okRef)
	assert.Regexp(t, `^\*lifecycle.Ref\[int\]\(0x[0-9a-f]+ starting\)$`, errRef)

	close(proceed)
	require.NoError(t, g.Shutdown(context.TODO()))

	assert.Regexp(t, `^\*lifecycle.Ref\[int\]\(0x[0-9a-f]+ 42\)$`, okRef)
	assert.Regexp(t, `^\*lifecycle.Ref\[int\]\(0x[0-9a-f]+ this didn't work\)$`, errRef)
}

func TestRef_RejectsBogusStuff(t *testing.T) {
	var g lifecycle.Group
	var zeroRef lifecycle.Ref[any]

	deref, err := zeroRef.Require(context.TODO())
	assert.Zero(t, deref)
	assert.ErrorContains(t, err, "context is not part of any lifecycle")

	var disposedCtx context.Context
	ref := lifecycle.GoFunc(&g, func(ctx context.Context) (*lifecycle.Task[any], error) {
		disposedCtx = ctx
		return lifecycle.TaskOf(nil, nil, any("bogus"))
	})

	var other lifecycle.Group
	other.Do(context.TODO(), func(ctx context.Context) {
		deref, err := ref.Require(ctx)
		assert.Zero(t, deref)
		assert.ErrorContains(t, err, "cannot require here: context is part of another lifecycle")
	})

	require.NoError(t, g.Shutdown(context.TODO()))

	deref, err = ref.Require(disposedCtx)
	assert.Zero(t, deref)
	assert.ErrorContains(t, err, "context's lifecycle scope has ended")

	deref, err = zeroRef.Require(disposedCtx)
	assert.Zero(t, deref)
	assert.ErrorContains(t, err, "invalid ref")
}

func TestRef_Require_ContextCancellation(t *testing.T) {
	var g lifecycle.Group

	testCtx, cancelTest := context.WithCancelCause(context.TODO())

	getDone := make(chan struct{})
	goroutineDone := make(chan struct{})
	ref := lifecycle.GoFunc(&g, func(ctx context.Context) (*lifecycle.Task[any], error) {
		defer close(goroutineDone)
		<-testCtx.Done()

		select {
		case <-ctx.Done():
			assert.Fail(t, "The goroutine's context is not independent of the outer context")
		default:
		}

		<-getDone
		return lifecycle.TaskOf(nil, nil, any("bogus"))
	})

	g.Do(testCtx, func(ctx context.Context) {
		defer close(getDone)
		time.AfterFunc(time.Microsecond, func() { cancelTest(assert.AnError) })
		deref, err := ref.Require(ctx)
		assert.Zero(t, deref)
		assert.ErrorIs(t, err, assert.AnError)
	})

	<-goroutineDone
}

func TestRef_Require_SelfDependency(t *testing.T) {
	var g lifecycle.Group
	provide := make(chan struct{})

	var self *lifecycle.Ref[any]
	self = lifecycle.GoFunc(&g, func(ctx context.Context) (*lifecycle.Task[any], error) {
		<-provide
		if _, err := self.Require(ctx); err != nil {
			return &lifecycle.Task[any]{Interface: "bogus"}, fmt.Errorf("self: %w", err)
		}
		return nil, nil
	})

	close(provide)

	g.Do(context.TODO(), func(ctx context.Context) {
		deref, err := self.Require(ctx)
		assert.Zero(t, deref)
		assert.ErrorContains(t, err, "self: circular dependency")
		assert.ErrorIs(t, err, lifecycle.ErrCircular)
	})
}

func TestRef_Require_LoopDetection(t *testing.T) {
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
			circle[i] = lifecycle.GoFunc(&g, func(ctx context.Context) (*lifecycle.Task[any], error) {
				<-provide
				for i := 0; i < order; i++ {
					<-seq[i]
				}

				msg := fmt.Sprintf("(order %d) %d awaits %d", order, i, j)
				time.AfterFunc(10*time.Microsecond, func() { close(seq[order]) })
				if _, err := circle[j].Require(ctx); err != nil {
					return nil, fmt.Errorf("%s: %w", msg, err)
				}
				return lifecycle.TaskOf(nil, nil, any(msg))
			})
		}

		close(provide)

		g.Do(context.TODO(), func(ctx context.Context) {
			for i := 0; i < order[len(order)-1]; i++ {
				<-seq[i]
			}
			close(seq[order[len(order)-1]])

			for i := range circle {
				order, i, j := order[i], i, (i+1)%len(circle)
				deref, err := circle[i].Require(ctx)
				assert.Zero(t, deref)
				assert.ErrorContains(t, err, fmt.Sprintf("(order %d) %d awaits %d: ", order, i, j))
				assert.ErrorIs(t, err, lifecycle.ErrCircular)
			}
		})

		return !t.Failed()
	})
}
