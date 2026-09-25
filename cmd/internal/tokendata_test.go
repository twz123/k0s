// SPDX-FileCopyrightText: 2025 k0s authors
// SPDX-License-Identifier: Apache-2.0

package internal

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/k0sproject/k0s/internal/secret"
	"github.com/k0sproject/k0s/pkg/token"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckSingleTokenSource(t *testing.T) {
	testToken := "test-token-data"

	t.Run("returns nil when no token sources provided", func(t *testing.T) {
		t.Setenv(EnvVarToken, "")

		err := CheckSingleTokenSource("", "")
		require.NoError(t, err)
	})

	t.Run("returns nil when only arg provided", func(t *testing.T) {
		t.Setenv(EnvVarToken, "")

		err := CheckSingleTokenSource(testToken, "")
		require.NoError(t, err)
	})

	t.Run("returns nil when only file provided", func(t *testing.T) {
		t.Setenv(EnvVarToken, "")

		err := CheckSingleTokenSource("", "/path/to/token")
		require.NoError(t, err)
	})

	t.Run("returns nil when only env provided", func(t *testing.T) {
		t.Setenv(EnvVarToken, testToken)

		err := CheckSingleTokenSource("", "")
		require.NoError(t, err)
	})

	t.Run("returns error when multiple token sources provided - env and arg", func(t *testing.T) {
		t.Setenv(EnvVarToken, testToken)

		err := CheckSingleTokenSource(testToken, "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "you can only pass one token source")
		assert.Contains(t, err.Error(), EnvVarToken)
	})

	t.Run("returns error when multiple token sources provided - env and file", func(t *testing.T) {
		t.Setenv(EnvVarToken, testToken)

		err := CheckSingleTokenSource("", "/path/to/token")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "you can only pass one token source")
	})

	t.Run("returns error when multiple token sources provided - arg and file", func(t *testing.T) {
		t.Setenv(EnvVarToken, "")

		err := CheckSingleTokenSource(testToken, "/path/to/token")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "you can only pass one token source")
	})

	t.Run("returns error when all three token sources provided", func(t *testing.T) {
		t.Setenv(EnvVarToken, testToken)

		err := CheckSingleTokenSource(testToken, "/path/to/token")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "you can only pass one token source")
	})
}

func TestGetTokenData_EnvVar(t *testing.T) {
	encoded, kubeconfig := encodedTestToken(t)

	t.Run("reads token from K0S_TOKEN env var", func(t *testing.T) {
		t.Setenv(EnvVarToken, encoded)

		tok, err := GetTokenData("", "")
		require.NoError(t, err)
		assert.Equal(t, kubeconfig, revealed(t, tok))
	})

	t.Run("empty K0S_TOKEN returns no token", func(t *testing.T) {
		t.Setenv(EnvVarToken, "")

		tok, err := GetTokenData("", "")
		require.NoError(t, err)
		assert.True(t, tok.IsZero(), "Expected no token")
	})
}

func TestGetTokenData_TokenArg(t *testing.T) {
	encoded, kubeconfig := encodedTestToken(t)

	t.Run("reads token from argument", func(t *testing.T) {
		t.Setenv(EnvVarToken, "")

		tok, err := GetTokenData(encoded, "")
		require.NoError(t, err)
		assert.Equal(t, kubeconfig, revealed(t, tok))
	})

	t.Run("fails for an undecodable token", func(t *testing.T) {
		t.Setenv(EnvVarToken, "")

		_, err := GetTokenData("not a token", "")
		assert.ErrorContains(t, err, "failed to decode join token")
	})
}

func TestGetTokenData_TokenFile(t *testing.T) {
	encoded, kubeconfig := encodedTestToken(t)

	t.Run("reads token from file", func(t *testing.T) {
		t.Setenv(EnvVarToken, "")

		tmpDir := t.TempDir()
		tokenFile := filepath.Join(tmpDir, "token")
		require.NoError(t, os.WriteFile(tokenFile, []byte(encoded), 0600))

		tok, err := GetTokenData("", tokenFile)
		require.NoError(t, err)
		assert.Equal(t, kubeconfig, revealed(t, tok))
	})

	t.Run("returns error for non-existent file", func(t *testing.T) {
		t.Setenv(EnvVarToken, "")

		_, err := GetTokenData("", "/non/existent/path")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "token file")
		assert.Contains(t, err.Error(), "not found")
		assert.Contains(t, err.Error(), "k0s token create")
	})

	t.Run("returns error for empty file", func(t *testing.T) {
		t.Setenv(EnvVarToken, "")

		tmpDir := t.TempDir()
		tokenFile := filepath.Join(tmpDir, "empty-token")
		require.NoError(t, os.WriteFile(tokenFile, []byte{}, 0600))

		_, err := GetTokenData("", tokenFile)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "token file")
		assert.Contains(t, err.Error(), "is empty")
		assert.Contains(t, err.Error(), "k0s token create")
	})
}

func TestGetTokenData_NoToken(t *testing.T) {
	t.Run("returns no token when no token provided", func(t *testing.T) {
		t.Setenv(EnvVarToken, "")

		tok, err := GetTokenData("", "")
		require.NoError(t, err)
		assert.True(t, tok.IsZero(), "Expected no token")
	})
}

// An encoded join token for tests, along with the kubeconfig it holds.
func encodedTestToken(t *testing.T) (string, []byte) {
	t.Helper()
	kubeconfig := []byte("the-kubeconfig")
	encoded, err := token.JoinToken{Value: secret.From[token.JoinToken](kubeconfig)}.RevealEncoded()
	require.NoError(t, err)
	return encoded, kubeconfig
}

// Reveals the given token, which is expected to be there.
func revealed(t *testing.T, tok token.JoinToken) []byte {
	t.Helper()
	kubeconfig, err := tok.Reveal()
	require.NoError(t, err)
	return kubeconfig
}
