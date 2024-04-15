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

package supervisor

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/k0sproject/k0s/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetEnv(t *testing.T) {
	env := []string{
		"only_generic=generic_value",
		"both=from_generic",
		"FOO_both=from_foo",
		"FOO_only_foo=foo_value",
		"HTTPS_PROXY=generic.example.com:8888",
		"FOO_HTTPS_PROXY=foo.example.com:1080",
		"PATH=/path/to/generic",
		"FOO_PATH=/path/to/foo",
	}

	t.Run("regular", func(t *testing.T) {
		expected := []string{
			"HTTPS_PROXY=foo.example.com:1080",
			fmt.Sprintf("PATH=/var/lib/k0s/bin%c/path/to/foo", os.PathListSeparator),
			"_K0S_MANAGED=yes",
			"both=from_foo",
			"only_foo=foo_value",
			"only_generic=generic_value",
		}

		testutil.Permute(env, func() bool {
			actual := getEnv(slices.Clone(env), "/var/lib/k0s", "foo", false)
			return assert.ElementsMatch(t, expected, actual, "for input: %v", env)
		})
	})

	t.Run("keepEnvPrefix", func(t *testing.T) {
		expected := []string{
			"FOO_PATH=/path/to/foo",
			"FOO_both=from_foo",
			"FOO_only_foo=foo_value",
			"HTTPS_PROXY=foo.example.com:1080",
			fmt.Sprintf("PATH=/var/lib/k0s/bin%c/path/to/generic", os.PathListSeparator),
			"_K0S_MANAGED=yes",
			"both=from_generic",
			"only_generic=generic_value",
		}

		testutil.Permute(env, func() bool {
			actual := getEnv(slices.Clone(env), "/var/lib/k0s", "foo", true)
			return assert.ElementsMatch(t, expected, actual, "for input: %v", env)
		})
	})

	t.Run("first variable wins", func(t *testing.T) {
		envs := [][]string{
			{"X=1" /**/, "X=2" /**/, "COMP_X=A", "COMP_X=B"},
			{"X=1" /**/, "COMP_X=A", "X=2" /**/, "COMP_X=B"},
			{"X=1" /**/, "COMP_X=A", "COMP_X=B", "X=2" /**/},
			{"COMP_X=A", "X=1" /**/, "COMP_X=B", "X=2" /**/},
			{"COMP_X=A", "COMP_X=B", "X=1" /**/, "X=2" /**/},
			{"COMP_X=A", "X=1" /**/, "X=2" /**/, "COMP_X=B"},
		}

		for _, env := range envs {
			expected := []string{"_K0S_MANAGED=yes", "X=A"}
			actual := getEnv(slices.Clone(env), "", "comp", false)
			require.ElementsMatch(t, expected, actual, "for input: %v", env)

			expected = []string{"_K0S_MANAGED=yes", "X=1", "COMP_X=A"}
			actual = getEnv(slices.Clone(env), "", "comp", true)
			require.ElementsMatch(t, expected, actual, "for input: %v", env)
		}
	})

	t.Run("add PATH if missing", func(t *testing.T) {
		dataDir := filepath.Join("path", "to", "data")
		binDir := filepath.Join(dataDir, "bin")
		expected := []string{"_K0S_MANAGED=yes", "PATH=" + binDir}
		actual := getEnv(nil, dataDir, "", false)
		assert.ElementsMatch(t, expected, actual, "for input: %v", env)
	})
}
