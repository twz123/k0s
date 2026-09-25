// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package secret

import (
	"fmt"
	"io"
	"os"
	"reflect"
	"strconv"
)

// Holds a secret value of type T in a way that makes it hard to use it
// unconsciously in an unsafe context. Formatting it yields a placeholder
// regardless of the verb. The value sits behind a pointer so that even
// reflection-driven printing, which bypasses methods for values reached via
// unexported fields, displays an address at most. The value is obtained via
// [Value.Reveal] and only via that. The zero value holds no secret value at
// all. [Value.IsZero] returns true, and [Value.Reveal] fails for it.
//
// Value is meant to be embedded. K is the embedding type, which lends its name
// to the placeholder, the bad verb marker and the zero value's error:
//
//	type Token struct {
//	    secret.Value[Token, string]
//	}
//
// K must be a named type for the name to be meaningful.
type Value[K, T any] struct {
	value *T
}

// Wraps the given value as a secret [Value] of kind K.
func From[K, T any](value T) Value[K, T] { return Value[K, T]{&value} }

// Reveals the secret value. It fails with a [NoValueError] for the zero value,
// which has nothing to reveal, and doesn't fail otherwise, so that an optional
// secret can be used where it's set and skipped where it's not:
//
//	if value, err := s.Reveal(); err == nil {
//	    use(value)
//	}
func (v Value[K, T]) Reveal() (T, error) {
	if v.value == nil {
		var zero T
		return zero, NoValueError[K]{}
	}
	return *v.value, nil
}

// IsZero indicates whether this is the zero value, which holds no secret value
// at all.
func (v Value[K, T]) IsZero() bool { return v.value == nil }

// Reveals the secret value to the given function and returns whatever that
// returns. It fails with a [NoValueError] for the zero value, like
// [Value.Reveal], without calling the function.
func (v Value[K, T]) Use[U any](f func(T) (U, error)) (U, error) {
	value, err := v.Reveal()
	if err != nil {
		var zero U
		return zero, err
	}
	return f(value)
}

// Stores the given value as the secret value, replacing whatever was stored
// before. The value is obtained via [Value.Reveal], and only via that.
func (v *Value[K, T]) Store(value T) { v.value = &value }

// Indicates that a zero secret value of kind K, which holds nothing, was to be
// revealed.
type NoValueError[K any] struct {
	// No state, since K says all there is to say.
}

// Error implements [error].
func (NoValueError[K]) Error() string { return "no " + nameOf[K]() }

// String implements [fmt.Stringer]. It never reveals the value.
func (Value[K, T]) String() string { return "<" + nameOf[K]() + ">" }

// Format implements [fmt.Formatter]. It never reveals the value, whatever the
// verb: the string verbs yield the placeholder, all others yield the usual bad
// verb marker, sans the value.
func (v Value[K, T]) Format(f fmt.State, verb rune) {
	switch verb {
	case 's', 'v':
		_, _ = io.WriteString(f, v.String())
	case 'q':
		_, _ = io.WriteString(f, strconv.Quote(v.String()))
	default:
		_, _ = io.WriteString(f, "%!"+string(verb)+"("+nameOf[K]()+")")
	}
}

// A secret string for values that need no kind of their own.
type String struct {
	Value[String, string]
}

// NoStringError is the [NoValueError] for the zero [String].
type NoStringError = NoValueError[String]

// Wraps the given string as a secret [String].
func FromString(value string) String { return String{From[String](value)} }

// Getenv wraps the value of the environment variable with the given key as a
// secret [String], so that it's never held in the open. The String is zero if
// the variable is unset or empty.
func Getenv(key string) (value String) {
	if secret := os.Getenv(key); secret != "" {
		value.Store(secret)
	}
	return
}

// ToBytes returns the string as secret [Bytes], which are zero for the zero
// String.
func (s String) ToBytes() (b Bytes) {
	if s := s.value; s != nil {
		b.value = new([]byte(*s))
	}
	return
}

// A secret byte slice for values that need no kind of their own.
type Bytes struct {
	Value[Bytes, []byte]
}

// NoBytesError is the [NoValueError] for the zero [Bytes].
type NoBytesError = NoValueError[Bytes]

// Wraps the given bytes as secret [Bytes].
func FromBytes(value []byte) Bytes { return Bytes{From[Bytes](value)} }

// Len returns the number of bytes, which is zero for the zero Bytes, as it is
// for a nil slice. It reveals the length, and only that.
func (b Bytes) Len() int {
	if b := b.value; b != nil {
		return len(*b)
	}
	return 0
}

// ReadFile reads the file with the given path as secret [Bytes], so that its
// contents are never held in the open.
func ReadFile(path string) (b Bytes, err error) {
	data, err := os.ReadFile(path)
	if err == nil {
		b.Store(data)
	}
	return
}

// Returns the name of the given type, as %T would print it.
func nameOf[K any]() string { return reflect.TypeFor[K]().String() }
