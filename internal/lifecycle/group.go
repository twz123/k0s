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

	"k8s.io/utils/ptr"
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

var ErrShutdown = errors.New("lifecycle shutdown")

// A Component can be started. It produces a [Unit].
type Component[T any] interface {
	Start(context.Context) (*Unit[T], error)
}

// A lifecycle unit as returned by [Component.Start].
// Use [TaskStarted], [ServiceStarted] or [Done] to create units.
type Unit[T any] struct {
	stop    chan<- struct{}
	done    <-chan struct{}
	outcome T
}

// A unit without an outcome.
type Task = Unit[struct{}]

// Returns a non-running unit with the given outcome.
// It won't participate in the lifecycle group's shutdown sequence.
// The error will always be nil.
func Done[T any](outcome T) (*Unit[T], error) {
	return &Unit[T]{outcome: outcome}, nil
}

// Returns a running unit without an outcome.
// The error will always be nil.
func TaskStarted(stop chan<- struct{}, done <-chan struct{}) (*Task, error) {
	return &Task{stop: stop, done: done}, nil
}

// Returns a running unit with the given outcome.
// The error will always be nil.
func ServiceStarted[T any](stop chan<- struct{}, done <-chan struct{}, t T) (*Unit[T], error) {
	return &Unit[T]{stop, done, t}, nil
}

type ComponentFunc[I any] func(context.Context) (*Unit[I], error)

func (f ComponentFunc[I]) Start(ctx context.Context) (*Unit[I], error) {
	return f(ctx)
}

type Ref[T any] struct {
	g       *Group
	node    *node
	started <-chan struct{}
	t       T
	err     error
}

type lifecycleNode struct {
	inner node

	cancelStart context.CancelCauseFunc
	started     <-chan struct{}

	// only valid when started
	stop chan<- struct{}
	done <-chan struct{}
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
		g:       g,
		node:    &node.inner,
		started: started,
	}

	go func() {
		g.withNodeContext(ctx, ref.node, func(ctx context.Context) {
			defer cancel(ref.err)
			if task, err := c.Start(ctx); err != nil {
				ref.err = err
			} else if task != nil {
				ref.t, node.stop, node.done = task.outcome, task.stop, task.done
			}
		})

		if ref.err == nil {
			close(started)
			return
		}

		err := fmt.Errorf("%w: failed to start: %w", ErrShutdown, ref.err)

		g.mu.Lock()
		defer g.mu.Unlock()
		close(started)
		if g.startErr == nil {
			g.startErr = err
		}
		if g.done == nil {
			done := make(chan struct{})
			g.done = done
			g.initiateShutdown(done, g.startErr)
		} else if g.cancelCompletion != nil {
			g.cancelCompletion(err)
		}
	}()

	return &ref
}

func GoFunc[T any](g *Group, start ComponentFunc[T]) *Ref[T] {
	return Go(g, start)
}

func (g *Group) withNodeContext(ctx context.Context, n *node, f func(context.Context)) {
	var p atomic.Pointer[*node]
	p.Store(ptr.To(n))
	defer p.Store(nil)
	f(context.WithValue(ctx, &g.mu, &p))
}

func (r *Ref[T]) Require(ctx context.Context) (t T, _ error) {
	if r.g == nil {
		return t, fmt.Errorf("invalid ref")
	}
	requiredNodePtr, ok := ctx.Value(&r.g.mu).(*atomic.Pointer[*node])
	if !ok {
		return t, fmt.Errorf("cannot require here: ref is not part of the context's lifecycle")
	}
	requiredNode := requiredNodePtr.Load()
	if requiredNode == nil {
		return t, fmt.Errorf("cannot require anymore: context's lifecycle scope has ended")
	}

	// Short-circuit if the ref failed to start.
	select {
	default:
	case <-r.started:
		if r.err != nil {
			return r.t, r.err
		}
	}

	// Add the dependency if the required node tracks its dependencies.
	// A nil required node indicates that it's a completion context.
	if *requiredNode != nil {
		if shutdown, err := r.g.addDependency(*requiredNode, r.node); shutdown {
			// The group is shutting down. If the referenced node's start method
			// returned with an error, prefer to return that one instead of the
			// shutdown error.
			select {
			default:
			case <-r.started:
				if r.err != nil {
					return r.t, r.err
				}
			}

			return t, cmp.Or(err, ErrShutdown)
		} else if err != nil {
			return t, err
		}
	}

	select {
	case <-ctx.Done():
		// Check if the context raced with the the referenced node's start
		// method. If that returned with an error, prefer to return that one
		// instead of the context's error.
		select {
		default:
		case <-r.started:
			if r.err != nil {
				return r.t, r.err
			}
		}

		return t, context.Cause(ctx)
	case <-r.started:
		return r.t, r.err
	}
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

	var err error
	g.withNodeContext(ctx, nil, func(ctx context.Context) {
		defer func() {
			go func() {
				g.mu.Lock()
				defer g.mu.Unlock()
				g.initiateShutdown(done, cmp.Or(g.startErr, ErrShutdown))
			}()
		}()

		err = f(ctx)
	})

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
	leaf.cancelStart(s.err)
	<-leaf.started

	if leaf.stop != nil {
		close(leaf.stop)
	}

	if leaf.done != nil {
		<-leaf.done
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

func (g *Group) addDependency(dependent, dependency *node) (shutdown bool, err error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.inShutdown {
		return true, g.startErr
	}

	return false, dependent.addDependency(dependency)
}

func (r *Ref[T]) String() string {
	// Having a Stringer implementation helps in
	// avoiding data races during logging.

	if r == nil {
		return fmt.Sprintf("%s<nil>", reflect.TypeOf(r))
	}

	select {
	case <-r.started:
	default:
		return fmt.Sprintf("%s(%p starting)", reflect.TypeOf(r), r.node)
	}

	if r.err != nil {
		return fmt.Sprintf("%s(%p %v)", reflect.TypeOf(r), r.node, r.err)
	} else {
		return fmt.Sprintf("%s(%p %v)", reflect.TypeOf(r), r.node, r.t)
	}
}
