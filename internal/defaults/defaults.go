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

package defaults

func From[T any](defaulter func(*T)) (t T) { defaulter(&t); return t }
func New[T any](defaulter func(*T)) *T     { t := new(T); defaulter(t); return t }

type Assigner[T any] interface {
	To(T) bool
	ToResult(func() T) bool
}

func IfEq[T comparable](field *T, target T) Assigner[T] {
	if *field == target {
		return Target[T]{field}
	}
	return Noop[T]{}
}

func IfZero[T comparable](field *T) Assigner[T] {
	var zero T
	return IfEq(field, zero)
}

type Noop[T any] struct{}

func (Noop[T]) To(T) bool                { return false }
func (Noop[T]) ToResult(f func() T) bool { return false }

type Target[T any] struct{ target *T }

func (a Target[T]) To(t T) bool              { *a.target = t; return true }
func (a Target[T]) ToResult(f func() T) bool { *a.target = f(); return true }

type PtrAssigner[T any] interface {
	Assigner[*T]
	ToPtrTo(T) bool
	ToNew() bool
}

func IfNil[T any](field **T) PtrAssigner[T] {
	if *field == nil {
		return TargetPtr[T]{field}
	}
	return NoopPtr[T]{}
}

type TargetPtr[T any] struct{ target **T }

func (a TargetPtr[T]) To(t *T) bool              { *a.target = t; return true }
func (a TargetPtr[T]) ToResult(f func() *T) bool { *a.target = f(); return true }
func (a TargetPtr[T]) ToPtrTo(t T) bool          { *a.target = &t; return true }
func (a TargetPtr[T]) ToNew() bool               { *a.target = new(T); return true }

type NoopPtr[T any] struct{}

func (NoopPtr[T]) To(*T) bool                { return false }
func (NoopPtr[T]) ToResult(f func() *T) bool { return false }
func (NoopPtr[T]) ToPtrTo(T) bool            { return false }
func (NoopPtr[T]) ToNew() bool               { return false }
