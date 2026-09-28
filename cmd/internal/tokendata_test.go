// SPDX-FileCopyrightText: 2025 k0s authors
// SPDX-License-Identifier: Apache-2.0

package internal

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetJoinTokenSource(t *testing.T) {
	const testToken = "test-token-data"

	t.Run("no source", func(t *testing.T) {
		t.Setenv(EnvVarToken, "")

		load, err := GetJoinTokenSource("", "")
		require.NoError(t, err)
		assert.Nil(t, load)
	})

	t.Run("argument", func(t *testing.T) {
		t.Setenv(EnvVarToken, "")

		load, err := GetJoinTokenSource(testToken, "")
		require.NoError(t, err)
		require.NotNil(t, load)

		token, err := load()
		require.NoError(t, err)
		assert.Equal(t, testToken, token)
	})

	t.Run("environment variable", func(t *testing.T) {
		t.Setenv(EnvVarToken, testToken)

		load, err := GetJoinTokenSource("", "")
		require.NoError(t, err)
		require.NotNil(t, load)

		token, err := load()
		require.NoError(t, err)
		assert.Equal(t, testToken, token)
	})

	t.Run("file", func(t *testing.T) {
		t.Setenv(EnvVarToken, "")
		tokenFile := filepath.Join(t.TempDir(), "token")

		load, err := GetJoinTokenSource("", tokenFile)
		require.NoError(t, err)
		require.NotNil(t, load)

		t.Run("is read lazily", func(t *testing.T) {
			require.NoError(t, os.WriteFile(tokenFile, []byte(testToken), 0600))

			token, err := load()
			require.NoError(t, err)
			assert.Equal(t, testToken, token)
		})

		t.Run("must not be empty", func(t *testing.T) {
			require.NoError(t, os.WriteFile(tokenFile, []byte{}, 0600))

			_, err := load()
			assert.ErrorContains(t, err, `token file "`+tokenFile+`" is empty`)
			assert.ErrorContains(t, err, "k0s token create")
		})

		t.Run("must exist", func(t *testing.T) {
			require.NoError(t, os.Remove(tokenFile))

			_, err := load()
			assert.ErrorContains(t, err, `token file "`+tokenFile+`" not found`)
			assert.ErrorContains(t, err, "k0s token create")
		})
	})

	t.Run("conflicts", func(t *testing.T) {
		for _, test := range []struct {
			name             string
			tokenArg, envVar string
			tokenFile        string
		}{
			{"argument and environment variable", testToken, testToken, ""},
			{"argument and file", testToken, "", "/path/to/token"},
			{"environment variable and file", "", testToken, "/path/to/token"},
			{"all three", testToken, testToken, "/path/to/token"},
		} {
			t.Run(test.name, func(t *testing.T) {
				t.Setenv(EnvVarToken, test.envVar)

				load, err := GetJoinTokenSource(test.tokenArg, test.tokenFile)
				assert.Nil(t, load)
				assert.ErrorContains(t, err, "you can only pass one token source")
				assert.ErrorContains(t, err, EnvVarToken)
			})
		}
	})
}
