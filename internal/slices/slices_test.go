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

package slices_test

import (
	"slices"
	"testing"

	internalslices "github.com/k0sproject/k0s/internal/slices"

	"github.com/stretchr/testify/assert"
)

func TestAllSubSlices(t *testing.T) {
	tests := []struct {
		input    []int
		expected [][]int
	}{
		{[]int{}, [][]int{{}}},
		{[]int{1}, [][]int{{}, {1}}},
		{[]int{1, 2}, [][]int{{}, {1}, {2}, {1, 2}}},
		{[]int{1, 2, 3}, [][]int{{}, {1}, {2}, {1, 2}, {3}, {1, 3}, {2, 3}, {1, 2, 3}}},
	}

	for _, test := range tests {
		result := slices.Collect(internalslices.AllSubSlices(test.input...))
		assert.ElementsMatch(t, test.expected, result)
	}
}
