// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package secret

import (
	"fmt"
	"io"
	"reflect"
	"strconv"
)

// Holds a secret value of type T. It is opaque: formatting it yields a
// placeholder, whatever the verb, and the value sits behind a pointer, so that
// even reflection-driven printing, which bypasses methods for values reached
// via unexported fields, shows an address at most. The value is obtained via
// [Value.Reveal], and only via that.
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
func Of[K, T any](value T) Value[K, T] {
	return Value[K, T]{&value}
}

// Reveals the secret value. It fails with a [NoValueError] for the zero Value,
// which holds no secret value at all.
func (v Value[K, T]) Reveal() (T, error) {
	if v.value == nil {
		var zero T
		return zero, NoValueError[K]{}
	}
	return *v.value, nil
}

// Indicates that a secret value kind K to be revealed hasn't been set.
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

// Returns the name of the given type, as %T would print it.
func nameOf[K any]() string {
	return reflect.TypeFor[K]().String()
}
