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

func TestGroup_AbortsWhenUnitsFailToStart(t *testing.T) {
	var g lifecycle.Group

	startErr := fmt.Errorf("from start func")
	okToStart := make(chan struct{})
	lifecycle.GoFunc(&g, func(ctx context.Context) (*lifecycle.Task, error) {
		<-okToStart // Ensure complete is called before start returns.
		return nil, startErr
	})

	completeErr := fmt.Errorf("from complete func")
	err := g.Complete(context.TODO(), func(ctx context.Context) error {
		close(okToStart)
		<-ctx.Done() // the context should get cancelled by the start error

		cause := context.Cause(ctx)
		assert.ErrorContains(t, cause, "lifecycle group is shutting down: failed to start: from start func")
		assert.ErrorIs(t, cause, lifecycle.ErrShutdown)
		assert.ErrorIs(t, cause, startErr)

		return completeErr
	})

	assert.Same(t, completeErr, err)
}

func TestGroup_AbortsEarlyWhenUnitsFailedToStart(t *testing.T) {
	var g lifecycle.Group

	startErr := fmt.Errorf("from start func")
	okToStart := make(chan struct{})
	start := lifecycle.GoFunc(&g, func(ctx context.Context) (*lifecycle.Task, error) {
		<-okToStart // Ensure the intermediate func is called before start returns.
		return nil, startErr
	})

	// Have some intermediate helper to ensure that start exits before complete is called.
	okToComplete := make(chan struct{})
	lifecycle.GoFunc(&g, func(ctx context.Context) (*lifecycle.Task, error) {
		close(okToStart)
		_, _ = start.Require(ctx)
		close(okToComplete)
		return nil, nil
	})

	<-okToComplete
	err := g.Complete(context.TODO(), func(ctx context.Context) error {
		require.Fail(t, "This func shouldn't be called")
		return assert.AnError
	})

	assert.ErrorContains(t, err, "lifecycle group is shutting down: failed to start: from start func")
	assert.ErrorIs(t, err, lifecycle.ErrShutdown)
	assert.ErrorIs(t, err, startErr)
}

func TestGroup_Shutdown(t *testing.T) {
	var g lifecycle.Group
	var stopped atomic.Uint32

	ones := lifecycle.GoFunc(&g, func(ctx context.Context) (*lifecycle.Unit[int], error) {
		stop, done := make(chan struct{}), make(chan struct{})
		go func() {
			defer close(done)
			<-stop
			assert.True(t, stopped.CompareAndSwap(1, 2), "ones not stopped after tens")
		}()

		return lifecycle.ServiceStarted(stop, done, 2)
	})

	tens := lifecycle.GoFunc(&g, func(ctx context.Context) (*lifecycle.Unit[int], error) {
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

		return lifecycle.ServiceStarted(stop, done, 40+ones)
	})

	var shutdownGoroutines sync.WaitGroup

	assert.NoError(t, g.Complete(context.TODO(), func(ctx context.Context) error {
		tens, err := tens.Require(ctx)
		if assert.NoError(t, err) {
			assert.Equal(t, 42, tens)
		}

		// Test brute force shutdown
		shutdown := make(chan struct{})
		for i := 0; i < 100; i++ {
			shutdownGoroutines.Add(1)
			go func() {
				defer shutdownGoroutines.Done()
				<-shutdown
				<-g.Shutdown()
			}()
		}

		close(shutdown)
		return nil
	}))

	shutdownGoroutines.Wait()
	assert.Equal(t, uint32(2), stopped.Load(), "Not all goroutines have been stopped in the right order")

	assert.PanicsWithValue(t, lifecycle.ErrShutdown, func() {
		lifecycle.GoFunc(&g, func(ctx context.Context) (*lifecycle.Task, error) {
			require.Fail(t, "u no call me once")
			return nil, nil
		})
	})
}

func TestRef_String(t *testing.T) {
	assert.Equal(t, "*lifecycle.Ref[int]<nil>", (*lifecycle.Ref[int])(nil).String())

	var g lifecycle.Group

	proceed := make(chan struct{})
	okRef := lifecycle.GoFunc(&g, func(ctx context.Context) (*lifecycle.Unit[int], error) {
		<-proceed
		return lifecycle.ServiceStarted(nil, nil, 42)
	})
	errRef := lifecycle.GoFunc(&g, func(ctx context.Context) (*lifecycle.Unit[int], error) {
		<-proceed
		return nil, fmt.Errorf("this didn't work")
	})

	assert.Regexp(t, `^\*lifecycle.Ref\[int\]\(0x[0-9a-f]+ starting\)$`, okRef)
	assert.Regexp(t, `^\*lifecycle.Ref\[int\]\(0x[0-9a-f]+ starting\)$`, errRef)

	close(proceed)
	<-g.Shutdown()

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
	ref := lifecycle.GoFunc(&g, func(ctx context.Context) (*lifecycle.Unit[any], error) {
		disposedCtx = ctx
		return lifecycle.ServiceStarted(nil, nil, any("bogus"))
	})

	var other lifecycle.Group
	assert.NoError(t, other.Complete(context.TODO(), func(ctx context.Context) error {
		deref, err := ref.Require(ctx)
		assert.Zero(t, deref)
		assert.ErrorContains(t, err, "cannot require here: context is part of another lifecycle")
		return nil
	}))

	<-g.Shutdown()

	deref, err = ref.Require(disposedCtx)
	assert.Zero(t, deref)
	assert.ErrorContains(t, err, "context's lifecycle scope has ended")

	deref, err = zeroRef.Require(disposedCtx)
	assert.Zero(t, deref)
	assert.ErrorContains(t, err, "invalid ref")
}

func TestRef_Require_ContextCancellation(t *testing.T) {
	t.Skip("FIXME Not working reliably yet")
	var g lifecycle.Group

	testCtx, cancelTest := context.WithCancelCause(context.TODO())

	getDone := make(chan struct{})
	goroutineDone := make(chan struct{})
	ref := lifecycle.GoFunc(&g, func(ctx context.Context) (*lifecycle.Unit[any], error) {
		defer close(goroutineDone)
		<-testCtx.Done()

		select {
		case <-ctx.Done():
			assert.Fail(t, "The goroutine's context is not independent of the outer context")
		default:
		}

		<-getDone
		return lifecycle.ServiceStarted(nil, nil, any("bogus"))
	})

	err := g.Complete(testCtx, func(ctx context.Context) error {
		defer close(getDone)
		time.AfterFunc(time.Microsecond, func() { cancelTest(assert.AnError) })
		deref, err := ref.Require(ctx)
		assert.Zero(t, deref)
		assert.Same(t, assert.AnError, err)
		return nil
	})

	assert.ErrorContains(t, err, "while waiting for lifecycle group to shutdown: ")
	assert.ErrorIs(t, err, assert.AnError)

	<-goroutineDone
}

func TestRef_Require_Twice(t *testing.T) {
	var g lifecycle.Group

	ref := lifecycle.GoFunc(&g, func(ctx context.Context) (*lifecycle.Task, error) {
		return nil, nil
	})

	uses := lifecycle.GoFunc(&g, func(ctx context.Context) (*lifecycle.Task, error) {
		_, err := ref.Require(ctx)
		assert.NoError(t, err)
		_, err = ref.Require(ctx)
		assert.NoError(t, err)

		// TODO: What happens if another component gets started here, and
		// that component uses some required refs from its outer component?
		// That component won't have the right lifecycle, i.e. won't be necessarily
		// stopped after the components of the outer ref?

		return nil, nil
	})

	assert.NoError(t, g.Complete(context.TODO(), func(ctx context.Context) error {
		_, err := uses.Require(ctx)
		assert.NoError(t, err)
		return nil
	}))
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

		circle := make([]*lifecycle.Ref[error], len(order)-1)
		for i := range circle {
			order, i, j := order[i], i, (i+1)%len(circle)
			circle[i] = lifecycle.GoFunc(&g, func(ctx context.Context) (*lifecycle.Unit[error], error) {
				<-provide
				for i := 0; i < order; i++ {
					<-seq[i]
				}

				time.AfterFunc(10*time.Microsecond, func() { close(seq[order]) })
				_, err := circle[j].Require(ctx)
				if err != nil {
					err = fmt.Errorf("(order %d) %d awaits %d: %w", order, i, j, err)
				}
				return lifecycle.ServiceStarted(nil, nil, err)
			})
		}

		close(provide)

		assert.NoError(t, g.Complete(context.TODO(), func(ctx context.Context) error {
			for i := 0; i < order[len(order)-1]; i++ {
				<-seq[i]
			}
			close(seq[order[len(order)-1]])

			var foundErrs int
			for i := range circle {
				order, i, j := order[i], i, (i+1)%len(circle)
				deref, err := circle[i].Require(ctx)
				assert.NoError(t, err)
				if deref != nil {
					foundErrs++
					assert.ErrorContains(t, deref, fmt.Sprintf("(order %d) %d awaits %d: ", order, i, j))
					assert.ErrorIs(t, deref, lifecycle.ErrSelfReferential)
				}
			}

			assert.Equal(t, 1, foundErrs)
			return nil
		}))

		return !t.Failed()
	})
}

func TestRef_Require_AcyclicDependencies(t *testing.T) {
	var g lifecycle.Group

	var started sync.WaitGroup
	stopped := make(chan string)

	// The startTask func is a helper that creates a unit that writes its name
	// to the stopped channel as soon as it's getting stopped. This is used
	// later on to detect the correct stop order.
	startTask := func(name string) (*lifecycle.Task, error) {
		stop, done := make(chan struct{}), make(chan struct{})
		go func() {
			started.Done()
			defer close(done)
			<-stop
			stopped <- name
		}()
		return lifecycle.TaskStarted(stop, done)
	}

	// Create the unit A
	started.Add(1)
	a := lifecycle.GoFunc(&g, func(ctx context.Context) (*lifecycle.Task, error) {
		return startTask("A")
	})

	// Create the unit B, which requires A
	started.Add(1)
	b := lifecycle.GoFunc(&g, func(ctx context.Context) (*lifecycle.Task, error) {
		_, err := a.Require(ctx)
		assert.NoError(t, err, "When B requires A")
		return startTask("B")
	})

	// Create the unit CBA, which first requires B, then A
	started.Add(1)
	lifecycle.GoFunc(&g, func(ctx context.Context) (*lifecycle.Task, error) {
		_, err := b.Require(ctx)
		assert.NoError(t, err, "When C requires B")
		_, err = a.Require(ctx)
		assert.NoError(t, err, "When C requires A after B")
		return startTask("CBA")
	})

	// Create the unit CAB, which first requires A, then B
	started.Add(1)
	lifecycle.GoFunc(&g, func(ctx context.Context) (*lifecycle.Task, error) {
		_, err := a.Require(ctx)
		assert.NoError(t, err, "When C requires A")
		_, err = b.Require(ctx)
		assert.NoError(t, err, "When C requires B after A")
		return startTask("CAB")
	})

	started.Wait()           // Wait until all units are started.
	shutdown := g.Shutdown() // Initiate the shutdown of all the units.

	require.ElementsMatch(t, []string{"CBA", "CAB"}, []string{<-stopped, <-stopped},
		"CBA and CAB should stop concurrently first, as nothing depends on them",
	)
	require.Equal(t, "B", <-stopped, "B should stop second, as its dependents CBA and CAB are stopped")
	require.Equal(t, "A", <-stopped, "A should stop last")

	close(stopped) // Just to ensure that it's no longer in use.
	<-shutdown     // Just to ensure that the shutdown process is done.
}

func TestRef_Require_CyclicDependencies(t *testing.T) {
	t.Skip("FIXME Not working reliably yet")
	var g lifecycle.Group

	var a, b, c *lifecycle.Ref[struct{}]
	start := make(chan struct{})
	var started sync.WaitGroup

	// Create the unit A, which requires C
	started.Add(1)
	a = lifecycle.GoFunc(&g, func(ctx context.Context) (*lifecycle.Task, error) {
		<-start
		time.AfterFunc(10*time.Millisecond, started.Done)
		_, err := c.Require(ctx)
		assert.NoError(t, err, "When A requires C")
		return nil, nil
	})

	// Create the unit B, which requires A
	started.Add(1)
	b = lifecycle.GoFunc(&g, func(ctx context.Context) (*lifecycle.Task, error) {
		<-start
		time.AfterFunc(10*time.Millisecond, started.Done)
		_, err := a.Require(ctx)
		assert.NoError(t, err, "When B requires A")
		return nil, nil
	})

	// Create the unit C, which requires the others, but can't
	c = lifecycle.GoFunc(&g, func(ctx context.Context) (*lifecycle.Task, error) {
		<-start
		started.Wait()
		_, err := a.Require(ctx)
		assert.ErrorContains(t, err, "self-referential direct dependency", "When C requires A")
		assert.ErrorIs(t, err, lifecycle.ErrSelfReferential, "When C requires A")
		_, err = b.Require(ctx)
		assert.ErrorContains(t, err, "self-referential circular dependency at depth 2", "When C requires B")
		assert.ErrorIs(t, err, lifecycle.ErrSelfReferential, "When C requires B")
		_, err = c.Require(ctx)
		assert.ErrorContains(t, err, "self-referential self-dependency", "When C requires itself")
		assert.ErrorIs(t, err, lifecycle.ErrSelfReferential, "When C requires itself")
		return nil, nil
	})

	close(start)

	g.Complete(context.TODO(), func(ctx context.Context) error {
		_, err := c.Require(ctx)
		assert.NoError(t, err, "When completing C")
		return nil
	})

	<-g.Shutdown()
}
