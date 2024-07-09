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

// Generic data structures for exchanging values between goroutines.
package value

// Allows for setting a value.
// Such a value is usually accessed via a [Getter] or [Peeker].
type Setter[T any] interface {
	Set(value T) // Sets the value.
}

// Allows the retrieval of a value.
// Such a value is usually previously set by a [Setter].
type Getter[T any] interface {
	// Retrieves the current value.
	// It depends on the implementation what happens if no value is available.
	// Implementations may either return a zero value or panic.
	Get() T
}

// Allows the retrieval of a value that may expire over time.
// Such a value is usually previously set by a [Setter].
type Peeker[T any] interface {
	Getter[T]

	// Retrieves the current value and its associated expiration channel.
	// Whenever the value expires, the expiration channel is closed and callers
	// can retrieve the updated value by calling [Peek] again.
	// It depends on the implementation what happens if no value is available.
	// Implementations may either return a zero value or panic.
	Peek() (value T, expired <-chan struct{})
}

type GetterFunc[T any] func() T

var _ Getter[any] = (GetterFunc[any])(nil)

// Get implements [Getter].
func (f GetterFunc[T]) Get() T {
	return f()
}

type XformedPeeker[T any, U any] struct {
	Inner Peeker[T]
	F     func(T) U
}

var _ Peeker[any] = (*XformedPeeker[struct{}, any])(nil)

// Get implements [Getter].
func (x *XformedPeeker[T, U]) Get() U {
	return x.F(x.Inner.Get())
}

// Peek implements [Peeker].
func (x *XformedPeeker[T, U]) Peek() (U, <-chan struct{}) {
	value, expired := x.Inner.Peek()
	return x.F(value), expired
}

// Allows to retrieve a value that may not yet exist.
// Such a value is usually previously set by a [Setter].
// The Ready channel is closed as soon as the value is available.
// It is only safe to retrieve the value when the Ready channel is closed.
type Eventual[T any] interface {
	// Returns a channel that is closed when the value is available.
	Ready() <-chan struct{}
	Getter[T]
}

// Awaits a value that may not yet exist. It returns the value and true if the
// value is set. If the done channel is closed before the value is available, it
// returns false.
func Await[T any](e Eventual[T], done <-chan struct{}) (T, bool) {
	if a, ok := e.(interface {
		Await(<-chan struct{}) (T, bool)
	}); ok {
		return a.Await(done)
	}

	select {
	case <-e.Ready():
		return e.Get(), true
	case <-done:
		var zero T
		return zero, false
	}
}

type val[T any] struct {
	inner T
	ch    chan struct{}
}
