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

package testutil_test

import (
	"fmt"
	"testing"

	"github.com/k0sproject/k0s/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPermute(t *testing.T) {
	input := []int{1, 2, 3, 4, 5}
	seen := map[string]struct{}{}

	testutil.Permute(input, func() bool {
		permutation := fmt.Sprint(input)
		_, exists := seen[permutation]
		require.False(t, exists, "Duplicate permutation: %s", permutation)
		seen[permutation] = struct{}{}
		return true
	})

	assert.Len(t, seen, 120)
}
