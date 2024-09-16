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

package testutil

// Permute calls f for each permutation of the given slice. The slice is
// reordered in-place before each call to f. f can achieve a premature
// termination of Permute by returning false. Permute will then stop and return
// false as well. Permute won't invoke f for nil or empty slices.
//
// Iterating over all permutations of a slice is sometimes useful for testing,
// to make sure that the outcome of some computation doesn't depend on the order
// of the input.
//
// Note that the permutations grow factorially, so the length of the slice
// should be reasonable (e.g. not longer than eight elements) to avoid
// combinatorial explosion.
func Permute[T any](slice []T, f func() bool) bool {
	switch len(slice) {
	default:
		sub := slice[1:]
		if !Permute(sub, f) {
			return false
		}
		for i := range sub {
			j := i + 1
			slice[0], slice[j] = slice[j], slice[0]
			if !Permute(sub, f) {
				return false
			}
			slice[0], slice[j] = slice[j], slice[0]
		}
	case 1:
		if !f() {
			return false
		}
	case 0:
	}

	return true
}
