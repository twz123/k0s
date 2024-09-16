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
	"slices"
	"sync"
	"sync/atomic"

	"github.com/sirupsen/logrus"
)

type Group struct {
	seq atomic.Uint32

	mu   sync.Mutex
	refs []any
}

type Slot struct {
	id atomic.Pointer[id]
}

type Ref[T any] struct {
	id   id
	done <-chan struct{}
	val  atomic.Pointer[result[T]]
}

type id struct {
	g   *Group
	seq uint32

	mu   sync.Mutex
	rels relations
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
		id:   id{g: g, seq: g.seq.Add(1)},
		done: done,
	}

	go func() {
		var r result[T]
		defer close(done)
		defer cancel(r.err)

		var slot Slot
		slot.id.Store(&ref.id)
		defer slot.id.Store(nil)

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
	slot.id.Store(&id{g: g, seq: g.seq.Add(1)})
	defer slot.id.Store(nil)
	f(ctx, &slot)
}

func Get[T any](ctx context.Context, slot *Slot, ref *Ref[T]) (t T, err error) {
	// FIXME Check all invariants between slot and ref here!

	if ref.id.g == nil {
		return t, fmt.Errorf("invalid ref")
	}

	slotID := slot.id.Load()
	if slotID == nil {
		return t, fmt.Errorf("invalid slot")
	}

	if slotID.g != ref.id.g {
		return t, fmt.Errorf("slot and ref incompatible")
	}

	// fast path
	select {
	case <-ctx.Done():
		return t, context.Cause(ctx)
	case <-ref.done:
		p := ref.val.Load()
		return p.t, p.err
	default:
		// fallback to slow path
	}

	if !ref.id.neededBy(slotID) {
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

func (needed *id) neededBy(depends *id) bool {
	// lock smaller sequences first to have a well-defined lock order
	if needed.seq < depends.seq {
		needed.mu.Lock()
		defer needed.mu.Unlock()
		depends.mu.Lock()
		defer depends.mu.Unlock()
	} else if depends.seq < needed.seq {
		depends.mu.Lock()
		defer depends.mu.Unlock()
		needed.mu.Lock()
		defer needed.mu.Unlock()
	} else {
		return false
	}

	if !depends.rels.addDependsOn(needed.seq) {
		return false
	}
	if !needed.rels.addNeededBy(depends.seq) {
		return false
	}

	for seq, rel := range needed.rels {
		if rel == dependsOn {
			if seq == depends.seq {
				return false
			}
			if !depends.rels.addDependsOn(seq) {
				return false
			}
		}
	}

	for seq, rel := range depends.rels {
		if rel == neededBy {
			if seq == needed.seq {
				return false
			}
			// FIXME is this right? It's not symmetric with the other for loop.
			if !depends.rels.addNeededBy(seq) {
				return false
			}
		}
	}

	logrus.Infof("slot %d %s", depends.seq, &depends.rels)

	return true
}

type relationType byte

const (
	dependsOn relationType = iota + 1
	neededBy
)

type relations map[uint32]relationType

func (r *relations) String() string {
	var needed, depends []uint32

	for seq, rel := range *r {
		switch rel {
		case neededBy:
			needed = append(needed, seq)
		case dependsOn:
			depends = append(depends, seq)
		}
	}

	slices.Sort(needed)
	slices.Sort(depends)

	return fmt.Sprintf("neededBy %v, depends on %v", needed, depends)
}

func (r *relations) addNeededBy(seq uint32) bool {
	if rel := (*r)[seq]; rel == dependsOn {
		return false
	}

	if *r == nil {
		*r = relations{seq: neededBy}
		return true
	}

	(*r)[seq] = neededBy
	return true
}

func (r *relations) isDependingOn(seq uint32) bool {
	return (*r)[seq] == dependsOn
}

func (r *relations) addDependsOn(seq uint32) bool {
	if rel := (*r)[seq]; rel == neededBy {
		return false
	}

	if *r == nil {
		*r = relations{seq: dependsOn}
		return true
	}

	(*r)[seq] = dependsOn
	return true
}
