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

package value

import "sync/atomic"

// An immutable value that will be available at some point.
//
// Use Once when producing a value asynchronously, and have a mechanism for
// consumers to wait for the value to become available in an interruptible
// manner.
//
// Example usage:
//
//	package main
//
//	import (
//	    "fmt"
//	    "sync"
//	    "time"
//
//	    "github.com/k0sproject/k0s/internal/sync/value"
//	)
//
//	func main() {
//	    // Declare a once value
//	    var once value.Once[string]
//
//	    // The value is not set yet, getting it will panic
//	    select {
//	    case <-once.Ready():
//	        panic("not set")
//	    default:
//	    }
//
//	    // Use a goroutine to wait for the value to become available
//	    var wg sync.WaitGroup
//	    wg.Add(1)
//	    go func() {
//	        defer wg.Done()
//	        <-once.Ready()
//	        fmt.Println(once.Get(), "world") // Output: Hello world!
//	    }()
//
//	    time.Sleep(1 * time.Second) // Simulate some delay
//	    // Set the value
//	    fmt.Println("Setting value once:", once.SetOnce("Hello")) // Output: Setting value once: true
//
//	    wg.Wait() // Wait for the watcher goroutine to finish
//
//	    // Attempt to set the value again
//	    fmt.Println("Setting value again:", once.SetOnce("world!")) // Output: Setting value again: false
//	}
type Once[T any] struct {
	p atomic.Pointer[val[*T]]
}

var _ Eventual[any] = (*Once[any])(nil)

// Sets the value if it is not already set.
// Returns false if the value has already been set.
func (o *Once[T]) SetOnce(value T) bool {
	for {
		var set val[*T]
		loaded := o.p.Load()
		switch {
		case loaded == nil:
			set = val[*T]{&value, make(chan struct{})}
		case loaded.inner == nil:
			set = val[*T]{&value, loaded.ch}
		default:
			return false
		}

		if o.p.CompareAndSwap(loaded, &set) {
			close(set.ch)
			return true
		}
	}
}

// Ready implements [Eventual].
// Returns a channel that is closed when the value is set.
func (o *Once[T]) Ready() <-chan struct{} {
	_, ch := o.get()
	return ch
}

// Waits until the value is set or the done channel is closed. Returns the value
// and true if the value is set, otherwise it returns the zero value and false.
func (o *Once[T]) Await(done <-chan struct{}) (T, bool) {
	v, ch := o.get()
	if v != nil {
		return *v, true
	}

	select {
	case <-ch:
		return *o.p.Load().inner, true
	case <-done:
		var zero T
		return zero, false
	}
}

// Get implements [Getter]. It retrieves the value if it is set.
// If the value is not set, it panics.
func (o *Once[T]) Get() T {
	if loaded := o.p.Load(); loaded != nil && loaded.inner != nil {
		return *loaded.inner
	}

	panic("value.Once: not set")
}

func (o *Once[T]) get() (*T, <-chan struct{}) {
	loaded := o.p.Load()
	if loaded == nil {
		loaded = &val[*T]{ch: make(chan struct{})}
		if !o.p.CompareAndSwap(nil, loaded) {
			loaded = o.p.Load()
		}
	}

	return loaded.inner, loaded.ch
}
