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
	"maps"
	"reflect"
	"sync"
	"sync/atomic"

	"k8s.io/utils/ptr"
)

type Group struct {
	mu       sync.Mutex
	nodes    []*lifecycleNode
	shutdown bool

	shutdownLock atomic.Pointer[<-chan struct{}]
}

type Slot struct {
	ptr atomic.Pointer[groupNode]
}

type Ref[T any] struct {
	groupNode

	started <-chan struct{}
	t       T
	err     error
}

type groupNode struct {
	*Group
	inner *node
}

type lifecycleNode struct {
	inner node

	cancelStart context.CancelCauseFunc
	started     <-chan struct{}
	stop        chan<- struct{}
	done        <-chan struct{}
}

var ErrShutdown = errors.New("lifecycle group is shutting down")

type ProviderFunc[T any] func(context.Context, *Slot) (t T, stop chan<- struct{}, done <-chan struct{}, err error)

func Go[T any](g *Group, f ProviderFunc[T]) *Ref[T] {
	ctx, cancel := context.WithCancelCause(context.Background())
	started := make(chan struct{})

	thisNode := &lifecycleNode{
		cancelStart: cancel,
		started:     started,
	}

	g.mu.Lock()
	if g.shutdown {
		g.mu.Unlock()
		close(started)
		return &Ref[T]{
			groupNode: groupNode{g, nil},
			err:       ErrShutdown,
		}
	}
	g.nodes = append(g.nodes, thisNode)
	g.mu.Unlock()

	ref := Ref[T]{
		groupNode: groupNode{g, &thisNode.inner},
		started:   started,
	}

	go func() {
		defer close(started)
		defer cancel(ref.err)

		var slot Slot
		slot.ptr.Store(&ref.groupNode)
		defer slot.ptr.Store(nil)

		ref.t, thisNode.stop, thisNode.done, ref.err = f(ctx, &slot)
	}()

	return &ref
}

func (g *Group) Do(ctx context.Context, f func(context.Context, *Slot)) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var slot Slot
	slot.ptr.Store(&groupNode{g, nil})
	defer slot.ptr.Store(nil)
	f(ctx, &slot)
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
		var newLeaves map[*node]*lifecycleNode

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

			case started:
				if leaf.stop != nil {
					close(leaf.stop)
					leaf.stop = nil
				}
				if len(selects) < 65535 {
					selectNodes = append(selectNodes, leaf)
					selects = append(selects, reflect.SelectCase{
						Dir:  reflect.SelectRecv,
						Chan: reflect.ValueOf(leaf.done),
					})
				}

			case done:
				delete(leaves, &leaf.inner)
				delete(remainingNodes, &leaf.inner)
				leaf.disposeLeaf(func(newLeaf *node) {
					mapSet(&newLeaves, newLeaf, remainingNodes[newLeaf])
				})
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
				mapSet(&newLeaves, newLeaf, remainingNodes[newLeaf])
			})
		}

		maps.Copy(leaves, newLeaves)
	}
}

func Get[T any](ctx context.Context, slot *Slot, ref *Ref[T]) (t T, err error) {
	if ref.Group == nil {
		return t, fmt.Errorf("invalid ref")
	}

	slotRef := slot.ptr.Load()
	if slotRef == nil {
		return t, fmt.Errorf("invalid slot")
	}

	if slotRef.Group != ref.Group {
		return t, fmt.Errorf("slot and ref incompatible")
	}

	if slotRef.inner != nil {
		if err := slotRef.addDependency(ref.inner); err != nil {
			return t, err
		}
	}

	if ref.started != nil {
		select {
		case <-ctx.Done():
			return t, context.Cause(ctx)
		case <-ref.started:
		}
	}

	return ref.t, ref.err
}

func (n *groupNode) addDependency(other *node) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.shutdown {
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

	if n.done != nil {
		select {
		case <-n.done:
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

func (s *Slot) String() string {
	if s == nil {
		return "<nil>"
	}

	if node := s.ptr.Load(); node != nil {
		return fmt.Sprintf("%s(%p)", reflect.TypeOf(s), node.inner)
	}

	return fmt.Sprintf("%s(invalid)", reflect.TypeOf(s))
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
