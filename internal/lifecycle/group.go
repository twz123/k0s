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

	"github.com/sirupsen/logrus"
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
	g *Group
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
			nodeRef: nodeRef{g, node{}},
			err:     ErrShutdown,
		}
	}
	g.nodes = append(g.nodes, thisNode)
	g.mu.Unlock()

	ref := Ref[T]{
		nodeRef: nodeRef{g, node{&thisNode.dependencies}},
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
	slot.ptr.Store(&nodeRef{g, node{}})
	defer slot.ptr.Store(nil)
	f(ctx, &slot)
}

func (g *Group) Shutdown(ctx context.Context) error {
	g.mu.Lock()
	g.shutdown = true
	g.mu.Unlock()

	shutdownChan := make(chan struct{})
	defer close(shutdownChan)

	log := logrus.WithField("lock", fmt.Sprintf("%p", &shutdownChan))

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

	type shutdownNode struct {
		*lifecycleNode
		dependents []*lifecycleNode
	}

	remainingNodes := make(map[*[]*node]*shutdownNode)
	for _, node := range g.nodes {
		sn := remainingNodes[&node.dependencies]
		if sn == nil {
			sn = new(shutdownNode)
			remainingNodes[&node.dependencies] = sn
		}
		if sn.lifecycleNode == nil {
			sn.lifecycleNode = node
		}

		for _, dependency := range node.dependencies {
			sn := remainingNodes[dependency.dependencies]
			if sn == nil {
				sn = new(shutdownNode)
				remainingNodes[dependency.dependencies] = sn
			}
			sn.dependents = append(sn.dependents, node)
		}
	}

	for {
		var selectNodes []*shutdownNode
		var selects []reflect.SelectCase

		for _, node := range remainingNodes {
			switch node.phase() {
			case starting:
				node.cancelStart(ErrShutdown)
				if len(selects) < 65535 {
					selectNodes = append(selectNodes, node)
					selects = append(selects, reflect.SelectCase{
						Dir:  reflect.SelectRecv,
						Chan: reflect.ValueOf(node.started),
					})
				}

			case started:
				var remainingDependents []*lifecycleNode
				for _, dependent := range node.dependents {
					if _, remaining := remainingNodes[&dependent.dependencies]; remaining {
						remainingDependents = append(remainingDependents, dependent)
					}
				}
				node.dependents = remainingDependents
				if len(node.dependents) > 0 {
					log.Info("Some dependents of ", node, " still running: ", node.dependents)
					continue
				}
				if node.stop != nil {
					log.Info("Stopping ", node)
					close(node.stop)
					node.stop = nil
				}
				if len(selects) < 65535 {
					selectNodes = append(selectNodes, node)
					selects = append(selects, reflect.SelectCase{
						Dir:  reflect.SelectRecv,
						Chan: reflect.ValueOf(node.done),
					})
				}

			case done:
				log.Info(node, done)
				delete(remainingNodes, &node.dependencies)
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
			log.Info(selectedNode, done)
			delete(remainingNodes, &selectedNode.dependencies)
		} else {
			log.Info(selectedNode, " not yet done")
		}
	}
}

func Get[T any](ctx context.Context, slot *Slot, ref *Ref[T]) (t T, err error) {
	if ref.g == nil {
		return t, fmt.Errorf("invalid ref")
	}

	slotRef := slot.ptr.Load()
	if slotRef == nil {
		return t, fmt.Errorf("invalid slot")
	}

	if slotRef.g != ref.g {
		return t, fmt.Errorf("slot and ref incompatible")
	}

	if slotRef.dependencies != nil {
		if err := slotRef.addDependency(&ref.node); err != nil {
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

func (n *nodeRef) addDependency(dependency *node) error {
	n.g.mu.Lock()
	defer n.g.mu.Unlock()

	if n.g.shutdown {
		return ErrShutdown
	}

	if dependency.dependsOn(&n.node) {
		return ErrCircular
	}

	*n.dependencies = append(*n.dependencies, dependency)
	return nil
}

func (n *node) dependsOn(dependency *node) bool {
	if n.dependencies == dependency.dependencies {
		return true
	}

	for _, candidate := range *n.dependencies {
		if candidate.dependsOn(dependency) {
			return true
		}
	}

	return false
}

type lifecyclePhase int

const (
	starting lifecyclePhase = iota
	started
	done
)

func (p lifecyclePhase) String() string {
	switch p {
	case starting:
		return "starting"
	case started:
		return "started"
	case done:
		return "done"
	default:
		return fmt.Sprintf("%s(%d)", reflect.TypeOf(p), p)
	}
}

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

func (r *Ref[T]) String() string {
	if r == nil {
		return "<nil>"
	}

	return fmt.Sprintf("%s(%p)", reflect.TypeOf(r), r.dependencies)
}

func (n *node) String() string {
	if n == nil {
		return "<nil>"
	}

	return fmt.Sprintf("%s(%p)", reflect.TypeOf(n), n.dependencies)
}

func (n *lifecycleNode) String() string {
	if n == nil {
		return "<nil>"
	}

	return fmt.Sprintf("%s(%p)", reflect.TypeOf(n), &n.dependencies)
}
