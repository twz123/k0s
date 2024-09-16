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

	"k8s.io/utils/ptr"
)

type Group struct {
	mu       sync.Mutex
	nodes    []*lifecycleNode
	shutdown bool

	shutdownChan atomic.Pointer[<-chan struct{}]
}

type Slot struct {
	ptr atomic.Pointer[nodeRef]
}

type Ref[T any] struct {
	nodeRef

	started <-chan struct{}
	t       T
	err     error
}

type node struct {
	dependencies *[]*node
}

type lifecycleNode struct {
	dependencies []*node

	cancelStart context.CancelCauseFunc
	started     <-chan struct{}
	stop        chan<- struct{}
	done        <-chan struct{}
}

type nodeRef struct {
	mu *sync.Mutex
	node
}

var ErrCircular = errors.New("circular dependency")
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
			nodeRef: nodeRef{&g.mu, node{}},
			err:     ErrShutdown,
		}
	}
	g.nodes = append(g.nodes, thisNode)
	g.mu.Unlock()

	ref := Ref[T]{
		nodeRef: nodeRef{&g.mu, node{&thisNode.dependencies}},
		started: started,
	}

	go func() {
		defer close(started)
		defer cancel(ref.err)

		var slot Slot
		slot.ptr.Store(&ref.nodeRef)
		defer slot.ptr.Store(nil)

		ref.t, thisNode.stop, thisNode.done, ref.err = f(ctx, &slot)
	}()

	return &ref
}

func (g *Group) Do(ctx context.Context, f func(context.Context, *Slot)) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var slot Slot
	slot.ptr.Store(&nodeRef{&g.mu, node{}})
	defer slot.ptr.Store(nil)
	f(ctx, &slot)
}

func (g *Group) Shutdown(ctx context.Context) error {
	g.mu.Lock()
	g.shutdown = true
	g.mu.Unlock()

	shutdownChan := make(chan struct{})
	defer close(shutdownChan)

	// FIXME is this a pattern that could be placed in some internal helper pkg?
	for !g.shutdownChan.CompareAndSwap(nil, ptr.To[<-chan struct{}](shutdownChan)) {
		shutdownChan := g.shutdownChan.Load()
		if shutdownChan == nil {
			select {
			case <-ctx.Done():
				return context.Cause(ctx)
			default:
				continue
			}
		}

		select {
		case <-(*shutdownChan):
			continue
		case <-ctx.Done():
			return context.Cause(ctx)
		}
	}
	defer g.shutdownChan.Store(nil)

	doneNodes := make(map[*[]*node]struct{}, len(g.nodes))

	for {
		var selects []reflect.SelectCase
		for _, node := range g.nodes {
			if _, ok := doneNodes[&node.dependencies]; ok {
				continue
			}

			for _, dependency := range node.dependencies {
				if _, ok := doneNodes[dependency.dependencies]; !ok {
					continue
				}
			}

			if node.cancelStart != nil {
				node.cancelStart(ErrShutdown)
				node.cancelStart = nil
			}

			if node.started != nil {
				select {
				case <-node.started:
					node.started = nil
				default:
					if len(selects) < 65535 {
						selects = append(selects, reflect.SelectCase{
							Dir:  reflect.SelectRecv,
							Chan: reflect.ValueOf(node.started),
						})
					}
					continue
				}
			}

			if node.stop != nil {
				close(node.stop)
				node.stop = nil
			}

			if node.done != nil {
				select {
				case <-node.done:
					node.done = nil
				default:
					if len(selects) < 65535 {
						selects = append(selects, reflect.SelectCase{
							Dir:  reflect.SelectRecv,
							Chan: reflect.ValueOf(node.done),
						})
					}
					continue
				}
			}

			doneNodes[&node.dependencies] = struct{}{}
		}

		if len(selects) < 1 {
			// FIXME sanity check. Can I somehow prove to myself that this is always the case?
			if dn, n := len(doneNodes), len(g.nodes); dn != n {
				panic(fmt.Sprintf("nothing to wait on, but only %d out of %d are done", dn, n))
			}
			return nil
		}

		selects = append(selects, reflect.SelectCase{
			Dir:  reflect.SelectRecv,
			Chan: reflect.ValueOf(ctx.Done()),
		})

		offset, _, _ := reflect.Select(selects)
		if offset == len(selects)-1 {
			return context.Cause(ctx)
		}

		node := g.nodes[offset]
		if node.done != nil {
			select {
			case <-node.done:
			default:
				continue
			}
		}
		doneNodes[&node.dependencies] = struct{}{}
	}
}

func Get[T any](ctx context.Context, slot *Slot, ref *Ref[T]) (t T, err error) {
	if ref.mu == nil {
		return t, fmt.Errorf("invalid ref")
	}

	slotRef := slot.ptr.Load()
	if slotRef == nil {
		return t, fmt.Errorf("invalid slot")
	}

	if slotRef.mu != ref.mu {
		return t, fmt.Errorf("slot and ref incompatible")
	}

	if slotRef.dependencies != nil {
		if err := slotRef.addDependency(&ref.nodeRef); err != nil {
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

func (n *nodeRef) addDependency(dependency *nodeRef) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	if dependency.node.dependsOn(&n.node) {
		return ErrCircular
	}

	*n.dependencies = append(*n.dependencies, &dependency.node)
	return nil
}

func (n *node) dependsOn(dependency *node) bool {
	if n.dependencies == dependency.dependencies {
		return true
	}

	for _, candidtate := range *n.dependencies {
		if candidtate.dependsOn(dependency) {
			return true
		}
	}

	return false
}
