// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package value

import (
	"sync/atomic"
)

type Once[T any] struct {
	p atomic.Pointer[val[T]]
}

func (f *Once[T]) IsSet() bool {
	if loaded := f.p.Load(); loaded != nil {
		select {
		case <-loaded.ch:
			return true
		default:
			return false
		}
	}

	return false
}

func (f *Once[T]) Done() <-chan struct{} {
	return f.val().ch
}

func (f *Once[T]) Await() T {
	val := f.val()
	<-f.val().ch
	return val.inner
}

func (f *Once[T]) val() *val[T] {
	loaded := f.p.Load()
	if loaded == nil {
		loaded = &val[T]{ch: make(chan struct{})}
		if !f.p.CompareAndSwap(nil, loaded) {
			loaded = f.p.Load()
		}
	}

	return loaded
}

func (f *Once[T]) Set(value T) bool {
	for {
		loaded := f.p.Load()
		if loaded != nil {
			select {
			case <-loaded.ch:
				return false
			default:
			}
		}

		set := &val[T]{value, make(chan struct{})}
		close(set.ch)

		if f.p.CompareAndSwap(loaded, set) {
			if loaded != nil {
				loaded.inner = value
				close(loaded.ch)
			}
			return true
		}
	}
}
