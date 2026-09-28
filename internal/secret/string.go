// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package secret

import "os"

// A secret string for values that need no kind of their own.
type String struct {
	Value[String, string]
}

// The [NoValueError] for the zero [String].
type NoStringError = NoValueError[String]

// Wraps the given string as a secret [String].
func FromString(value string) String { return String{From[String](value)} }

// ToBytes returns the string as secret [Bytes], which are zero for the zero
// String.
func (s String) ToBytes() (b Bytes) {
	if s.reveal != nil {
		b.Store([]byte(s.reveal()))
	}
	return
}

// Getenv wraps the value of the environment variable with the given key as a
// secret [String], so that it's never held in the open. The String is zero if
// the variable is unset or empty.
func Getenv(key string) (value String) {
	if secret := os.Getenv(key); secret != "" {
		value.Store(secret)
	}
	return
}
