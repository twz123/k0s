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

package slices

import (
	"iter"
	"math/bits"

	"golang.org/x/exp/constraints"
)

// Yields the elements of s along with their 0-base element index.
// This is a way to make s behave as if it was a slice in range loops.
func Enumerate[I constraints.Integer, E any](s iter.Seq[E]) iter.Seq2[I, E] {
	return func(yield func(I, E) bool) {
		var i I
		for e := range s {
			if !yield(i, e) {
				return
			}
			i++
		}
	}
}

// Yields all sub-slices of the given slice. In other words, AllSubSlices
// computes the mathematical power set of s by yielding all of the power set's
// elements.
//
// Note that the cardinality of the power set grows exponentially (2 ^ len(s)),
// so the length of s should be reasonable to avoid combinatorial explosion. If
// the length of s exceeds 64, AllSubSlices will panic.
func AllSubSlices[E any](s ...E) iter.Seq[[]E] {
	return func(yield func([]E) bool) {
		len := len(s)
		if len > 64 { // More bits than uint64 provides?
			panic("slice too large")
		}

		for i, len := uint64(0) /* 2^n subsets: */, uint64(1)<<len; i < len; i++ {
			// The 1-bits in p select which elements of s will be included.
			sub := make([]E, bits.OnesCount64(i))
			n := i // Use bit-clearing to iterate over the bit indices.
			for i := range sub {
				// The next index to append is the one with the lowest bit set.
				idx := bits.TrailingZeros64(n)
				sub[i] = s[idx]
				n &^= (1 << idx) // Clears the lowest set bit in n.
			}
			if !yield(sub) {
				return
			}
		}
	}
}
