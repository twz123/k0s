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
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"

	"github.com/k0sproject/k0s/pkg/k0scontext"
	"k8s.io/utils/ptr"
)

// TODO: Think about the ordering of the symbols in this file.

type Group struct {
	mu       sync.Mutex
	nodes    []*lifecycleNode
	shutdown bool

	shutdownLock atomic.Pointer[<-chan struct{}]
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

// type Provider[T any] interface {
// 	Provide() (T, error)
// }

// A Component can be started. It produces a task.  I represents the task's interface.
type Component[I any] interface {
	Start(context.Context) (*Task[I], error)
}

// Convenience function to create a task and let the compiler do the type inference.
// The error will always be nil.
func TaskOf[I any](stop chan<- struct{}, done <-chan struct{}, i I) (*Task[I], error) {
	return &Task[I]{TaskHandle{stop, done}, i}, nil
}

type ComponentFunc[I any] func(context.Context) (*Task[I], error)

func (f ComponentFunc[I]) Start(ctx context.Context) (*Task[I], error) {
	return f(ctx)
}

type Ref[T any] struct {
	// FIXME: Split this in some "untyped" ref
	// and let the "typed ref" embed the untyped stuff.

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
	if g.shutdown {
		g.mu.Unlock()
		return &Ref[T]{groupNode: groupNode{g, nil}, err: ErrShutdown}
	}
	g.nodes = append(g.nodes, node)
	g.mu.Unlock()

	ref := Ref[T]{
		groupNode: groupNode{g, &node.inner},
		started:   started,
	}

	go func() {
		defer close(started)
		defer cancel(ref.err)

		var ptr atomic.Pointer[groupNode]
		ptr.Store(&ref.groupNode)
		defer ptr.Store(nil)

		task, err := c.Start(k0scontext.WithValue(ctx, (*slotT)(&ptr)))
		if err != nil {
			ref.err = err
		} else if task != nil {
			ref.t, node.handle = task.Interface, task.TaskHandle
		}
	}()

	return &ref
}

func GoFunc[T any](g *Group, start ComponentFunc[T]) *Ref[T] {
	return Go(g, start)
}

type slotT atomic.Pointer[groupNode]

func (r *Ref[T]) Require(ctx context.Context) (t T, _ error) {
	ptr := (*atomic.Pointer[groupNode])(k0scontext.Value[*slotT](ctx))

	if ptr == nil {
		return t, fmt.Errorf("cannot require here: context is not part of any lifecycle")
	}

	if r.g == nil {
		return t, fmt.Errorf("invalid ref")
	}

	// FIXME what about nested stuff here????
	// I.e. a nested component tries to lookup something from a parent.

	node := ptr.Load()
	if node == nil {
		return t, fmt.Errorf("cannot require anymore: context's lifecycle scope has ended")
	}

	if node.g != r.g {
		return t, fmt.Errorf("cannot require here: context is part of another lifecycle")
	}

	if node.inner != nil {
		if err := node.addDependency(r.inner); err != nil {
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

func (g *Group) Do(ctx context.Context, f func(context.Context)) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var ptr atomic.Pointer[groupNode]
	ptr.Store(&groupNode{g, nil})
	defer ptr.Store(nil)

	f(k0scontext.WithValue(ctx, (*slotT)(&ptr)))
}

func (g *Group) Shutdown(ctx context.Context) error {
	g.mu.Lock()
	g.shutdown = true
	g.mu.Unlock()

	lock := make(chan struct{})
	defer close(lock)

	// FIXME is this a pattern that could be placed in some internal helper pkg?
	for !g.shutdownLock.CompareAndSwap(nil, ptr.To[<-chan struct{}](lock)) {
		shutdownLock := g.shutdownLock.Load()
		if shutdownLock == nil {
			select {
			case <-ctx.Done():
				return context.Cause(ctx)
			default:
				continue
			}
		}

		select {
		case <-(*shutdownLock):
			continue
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
	defer g.shutdownLock.Store(nil)

	if len(g.nodes) < 1 {
		return nil
	}

	remainingNodes := make(map[*node]*lifecycleNode, len(g.nodes))
	leaves := make(map[*node]*lifecycleNode)
	for _, node := range g.nodes {
		remainingNodes[&node.inner] = node
		if !node.inner.hasRelations(dependent) {
			leaves[&node.inner] = node
		}
	}

	for {
		var selectNodes []*lifecycleNode
		var selects []reflect.SelectCase

		for _, leaf := range leaves {
			switch leaf.phase() {
			case starting:
				leaf.cancelStart(ErrShutdown)
				if len(selects) < 65535 {
					selectNodes = append(selectNodes, leaf)
					selects = append(selects, reflect.SelectCase{
						Dir:  reflect.SelectRecv,
						Chan: reflect.ValueOf(leaf.started),
					})
				}

			case started, done /* will be disposed when selected */ :
				if leaf.handle.Stop != nil {
					close(leaf.handle.Stop)
					leaf.handle.Stop = nil
				}
				if len(selects) < 65535 {
					selectNodes = append(selectNodes, leaf)
					selects = append(selects, reflect.SelectCase{
						Dir:  reflect.SelectRecv,
						Chan: reflect.ValueOf(leaf.handle.Done),
					})
				}
			}
		}

		if len(selects) < 1 {
			// FIXME sanity check. Can I somehow prove to myself that this is always the case?
			if n := len(remainingNodes); n > 0 {
				panic(fmt.Sprintf("nothing to wait on, but %d are still pending", n))
			}

			g.nodes = nil
			return nil
		}

		selects = append(selects, reflect.SelectCase{
			Dir:  reflect.SelectRecv,
			Chan: reflect.ValueOf(ctx.Done()),
		})

		index, _, _ := reflect.Select(selects)
		if index == len(selects)-1 {
			return context.Cause(ctx)
		}

		if selectedNode := selectNodes[index]; selectedNode.phase() == done {
			delete(leaves, &selectedNode.inner)
			delete(remainingNodes, &selectedNode.inner)
			selectedNode.disposeLeaf(func(newLeaf *node) {
				leaves[newLeaf] = remainingNodes[newLeaf]
			})
		}
	}
}

func (n *groupNode) addDependency(other *node) error {
	n.g.mu.Lock()
	defer n.g.mu.Unlock()

	if n.g.shutdown {
		return ErrShutdown
	}

	return n.inner.add(dependency, other)
}

type lifecyclePhase int

const (
	starting lifecyclePhase = iota
	started
	done
)

func (n *lifecycleNode) phase() lifecyclePhase {
	if n.started != nil {
		select {
		case <-n.started:
		default:
			return starting
		}
	}

	if n.handle.Done != nil {
		select {
		case <-n.handle.Done:
		default:
			return started
		}
	}

	return done
}

func (n *lifecycleNode) disposeLeaf(consumeNewLeaf func(*node)) {
	for related := range n.inner.edges {
		delete(related.edges, &n.inner)
		if !related.hasRelations(dependent) {
			consumeNewLeaf(related)
		}
	}
	n.inner.edges = nil
	return
}

func (r *Ref[T]) String() string {
	// Having a Stringer implementation helps in
	// avoiding data races during logging.

	if r == nil {
		return "<nil>"
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
