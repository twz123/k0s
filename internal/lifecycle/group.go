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

package lifecycle

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"

	"github.com/k0sproject/k0s/pkg/k0scontext"
)

// TODO: Think about the ordering of the symbols in this file.

type Group struct {
	mu               sync.Mutex
	nodes            []*lifecycleNode
	done             <-chan struct{}
	cancelCompletion context.CancelCauseFunc
	startErr         error
	inShutdown       bool
}

var ErrShutdown = errors.New("lifecycle group is shutting down")

// The handle to a task.
type TaskHandle struct {
	Stop chan<- struct{} // Will be closed to signal that this task should stop.
	Done <-chan struct{} // Will be closed the task has stopped.
}

// A task that exposes some sort of interface to interact with it.
type Task[I any] struct {
	TaskHandle
	Interface I // The task's interface.
}

// A task that doesn't have any interface to interact with.
type Unit = Task[struct{}]

// A Component can be started. It produces a [Task]. I represents the task's interface.
type Component[I any] interface {
	Start(context.Context) (*Task[I], error)
}

func Started(stop chan<- struct{}, done <-chan struct{}) (*Task[struct{}], error) {
	return &Task[struct{}]{TaskHandle{stop, done}, struct{}{}}, nil
}

// Convenience function to create a task and let the compiler do the type inference.
// The error will always be nil.
func StartedWith[I any](stop chan<- struct{}, done <-chan struct{}, i I) (*Task[I], error) {
	return &Task[I]{TaskHandle{stop, done}, i}, nil
}

type ComponentFunc[I any] func(context.Context) (*Task[I], error)

func (f ComponentFunc[I]) Start(ctx context.Context) (*Task[I], error) {
	return f(ctx)
}

type Ref[T any] struct {
	groupNode

	started <-chan struct{}
	t       T
	err     error
}

type groupNode struct {
	g     *Group
	inner *node
}

type lifecycleNode struct {
	inner node

	cancelStart context.CancelCauseFunc
	started     <-chan struct{}
	handle      TaskHandle // only valid after started closed
}

func Go[T any](g *Group, c Component[T]) *Ref[T] {
	ctx, cancel := context.WithCancelCause(context.Background())
	started := make(chan struct{})

	node := &lifecycleNode{
		cancelStart: cancel,
		started:     started,
	}

	g.mu.Lock()
	if g.done != nil {
		startErr := g.startErr
		g.mu.Unlock()
		panic(cmp.Or(startErr, ErrShutdown))
	}
	g.nodes = append(g.nodes, node)
	g.mu.Unlock()

	ref := Ref[T]{
		groupNode: groupNode{g, &node.inner},
		started:   started,
	}

	go func() {
		defer close(started)

		func() {
			var ptr ctxPtr
			ptr.Store(&ref.groupNode)
			defer func() {
				ptr.Store(nil)
				cancel(ref.err)
			}()

			if task, err := c.Start(k0scontext.WithValue(ctx, &ptr)); err != nil {
				ref.err = err
			} else if task != nil {
				ref.t, node.handle = task.Interface, task.TaskHandle
			}
		}()

		if ref.err == nil {
			return
		}

		err := fmt.Errorf("%w: failed to start: %w", ErrShutdown, ref.err)
		g.mu.Lock()
		defer g.mu.Unlock()
		if g.startErr == nil {
			g.startErr = err
		}
		if g.done == nil {
			done := make(chan struct{})
			g.done = done
			g.initiateShutdown(done, g.startErr)
		} else {
			if g.cancelCompletion != nil {
				g.cancelCompletion(err)
			}
		}
	}()

	return &ref
}

func GoFunc[T any](g *Group, start ComponentFunc[T]) *Ref[T] {
	return Go(g, start)
}

// A pointer to a node, stored in a context.
type ctxPtr = atomic.Pointer[groupNode]

func (r *Ref[T]) Require(ctx context.Context) (t T, _ error) {
	ptr := k0scontext.Value[*ctxPtr](ctx)

	if ptr == nil {
		return t, fmt.Errorf("cannot require here: context is not part of any lifecycle")
	}

	if r.g == nil {
		return t, fmt.Errorf("invalid ref")
	}

	ctxNode := ptr.Load()
	if ctxNode == nil {
		return t, fmt.Errorf("cannot require anymore: context's lifecycle scope has ended")
	}

	if ctxNode.g != r.g {
		return t, fmt.Errorf("cannot require here: context is part of another lifecycle")
	}

	if ctxNode.inner != nil {
		if err := ctxNode.addDependency(r.inner); err != nil {
			return t, err
		}
	}

	if r.started != nil {
		select {
		case <-ctx.Done():
			return t, context.Cause(ctx)
		case <-r.started:
		}
	}

	return r.t, r.err
}

func (g *Group) Complete(ctx context.Context, f func(ctx context.Context) error) error {
	g.mu.Lock()
	if g.done != nil {
		startErr := g.startErr
		g.mu.Unlock()
		return cmp.Or(startErr, ErrShutdown)
	}

	done := make(chan struct{})
	g.done = done
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	g.cancelCompletion = cancel
	g.mu.Unlock()

	beginShutdown := make(chan struct{})
	defer close(beginShutdown)
	go func() {
		<-beginShutdown
		g.mu.Lock()
		defer g.mu.Unlock()
		g.initiateShutdown(done, cmp.Or(g.startErr, ErrShutdown))
	}()

	var ptr ctxPtr
	ptr.Store(&groupNode{g, nil})
	defer ptr.Store(nil)

	err := f(k0scontext.WithValue(ctx, &ptr))
	beginShutdown <- struct{}{}

	select {
	case <-done:
		if err == nil {
			return g.startErr
		}
	case <-ctx.Done():
		if err == nil {
			return fmt.Errorf("while waiting for lifecycle group to shutdown: %w", context.Cause(ctx))
		}
	}

	return err
}

func (g *Group) Shutdown() <-chan struct{} {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.done == nil {
		done := make(chan struct{})
		g.done = done
		g.initiateShutdown(done, ErrShutdown)
	}

	return g.done
}

func (g *Group) initiateShutdown(done chan<- struct{}, err error) {
	g.inShutdown = true

	numNodes := len(g.nodes)
	if numNodes < 1 {
		close(done)
		return
	}

	s := nodeShutdown{done, err, &g.mu, make(map[*node]*lifecycleNode, numNodes)}
	for _, node := range g.nodes {
		s.remainingNodes[&node.inner] = node
		if len(node.inner.dependents) < 1 {
			go s.disposeLeaf(node)
		}
	}

	clear(g.nodes)
	g.nodes = nil
}

type nodeShutdown struct {
	done           chan<- struct{}
	err            error
	mu             *sync.Mutex
	remainingNodes map[*node]*lifecycleNode
}

func (s *nodeShutdown) disposeLeaf(leaf *lifecycleNode) {
	if leaf.started != nil {
		leaf.cancelStart(s.err)
		<-leaf.started
	}

	if leaf.handle.Stop != nil {
		close(leaf.handle.Stop)
		leaf.handle.Stop = nil
	}

	if leaf.handle.Done != nil {
		<-leaf.handle.Done
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	leaf.inner.disposeLeaf(func(newLeaf *node) {
		go s.disposeLeaf(s.remainingNodes[newLeaf])
	})

	if len(s.remainingNodes) > 1 {
		delete(s.remainingNodes, &leaf.inner)
	} else {
		clear(s.remainingNodes)
		s.remainingNodes = nil
		close(s.done)
	}
}

func (n *groupNode) addDependency(other *node) error {
	n.g.mu.Lock()
	defer n.g.mu.Unlock()

	if n.g.inShutdown {
		return cmp.Or(n.g.startErr, ErrShutdown)
	}

	return n.inner.addDependency(other)
}

func (r *Ref[T]) String() string {
	// Having a Stringer implementation helps in
	// avoiding data races during logging.

	if r == nil {
		return fmt.Sprintf("%s<nil>", reflect.TypeOf(r))
	}

	if r.started != nil {
		select {
		case <-r.started:
		default:
			return fmt.Sprintf("%s(%p starting)", reflect.TypeOf(r), r.inner)
		}
	}

	if r.err != nil {
		return fmt.Sprintf("%s(%p %v)", reflect.TypeOf(r), r.inner, r.err)
	} else {
		return fmt.Sprintf("%s(%p %v)", reflect.TypeOf(r), r.inner, r.t)
	}
}
