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
	"sync"
	"sync/atomic"
)

type Group struct {
	// seq atomic.Uint32

	mu   sync.Mutex
	refs []any
}

type Slot struct {
	node atomic.Pointer[node]
}

type Ref[T any] struct {
	node node
	done <-chan struct{}
	val  atomic.Pointer[result[T]]
}

type node struct {
	g *Group
	// seq uint32

	// mu    sync.Mutex
	dependencies, dependents []*node

	// rels relations
}

type result[T any] struct {
	t    T
	stop chan<- struct{}
	done <-chan struct{}
	err  error
}

var ErrCircular = errors.New("circular dependency")

type ProviderFunc[T any] func(context.Context, *Slot) (t T, stop chan<- struct{}, done <-chan struct{}, err error)

func Go[T any](g *Group, f ProviderFunc[T]) *Ref[T] {
	ctx, cancel := context.WithCancelCause(context.Background())
	done := make(chan struct{})

	ref := Ref[T]{
		node: node{g: g},
		done: done,
	}

	go func() {
		var r result[T]
		defer close(done)
		defer cancel(r.err)

		var slot Slot
		slot.node.Store(&ref.node)
		defer slot.node.Store(nil)

		r.t, r.stop, r.done, r.err = f(ctx, &slot)
		ref.val.Store(&r)
	}()

	g.mu.Lock()
	defer g.mu.Unlock()
	g.refs = append(g.refs, &ref)

	return &ref
}

func (g *Group) Do(ctx context.Context, f func(context.Context, *Slot)) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var slot Slot
	slot.node.Store(&node{g: g})
	defer slot.node.Store(nil)
	f(ctx, &slot)
}

func Get[T any](ctx context.Context, slot *Slot, ref *Ref[T]) (t T, err error) {
	// FIXME Check all invariants between slot and ref here!

	if ref.node.g == nil {
		return t, fmt.Errorf("invalid ref")
	}

	slotID := slot.node.Load()
	if slotID == nil {
		return t, fmt.Errorf("invalid slot")
	}

	if slotID.g != ref.node.g {
		return t, fmt.Errorf("slot and ref incompatible")
	}

	if !slotID.addDependency(&ref.node) {
		return t, ErrCircular
	}

	select {
	case <-ctx.Done():
		return t, context.Cause(ctx)
	case <-ref.done:
		p := ref.val.Load()
		return p.t, p.err
	}
}

func (candidate *node) addDependency(dependency *node) bool {
	candidate.g.mu.Lock()
	defer candidate.g.mu.Unlock()

	if dependency.dependsOn(candidate) {
		return false
	}

	candidate.dependencies = append(candidate.dependencies, dependency)
	return true
}

func (candidate *node) dependsOn(dependency *node) bool {
	if candidate == dependency {
		return true
	}

	for _, candidtate := range candidate.dependencies {
		if candidtate.dependsOn(dependency) {
			return true
		}
	}

	return false
}
