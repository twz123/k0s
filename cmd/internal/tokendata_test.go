// SPDX-FileCopyrightText: 2025 k0s authors
// SPDX-License-Identifier: Apache-2.0

package internal

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/k0sproject/k0s/internal/secret"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckSingleTokenSource(t *testing.T) {
	t.Run("no error when no token sources provided", func(t *testing.T) {
		t.Setenv(EnvVarToken, "")

		err := CheckSingleTokenSource(secret.String{}, "")
		require.NoError(t, err)
	})

	t.Run("no error when only arg provided", func(t *testing.T) {
		t.Setenv(EnvVarToken, "")

		err := CheckSingleTokenSource(secret.FromString("some-token-arg"), "")
		require.NoError(t, err)
	})

	t.Run("no error when only file provided", func(t *testing.T) {
		t.Setenv(EnvVarToken, "")

		err := CheckSingleTokenSource(secret.String{}, "/path/to/token")
		require.NoError(t, err)
	})

	t.Run("no error when only env provided", func(t *testing.T) {
		t.Setenv(EnvVarToken, "some-token")

		err := CheckSingleTokenSource(secret.String{}, "")
		require.NoError(t, err)
	})

	t.Run("fails on multiple token sources provided - env and arg", func(t *testing.T) {
		t.Setenv(EnvVarToken, "some-token")

		err := CheckSingleTokenSource(secret.FromString("some-token-arg"), "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "you can only pass one token source")
		assert.Contains(t, err.Error(), EnvVarToken)
	})

	t.Run("returns error when multiple token sources provided - env and file", func(t *testing.T) {
		t.Setenv(EnvVarToken, "some-token")

		err := CheckSingleTokenSource(secret.String{}, "/path/to/token")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "you can only pass one token source")
	})

	t.Run("returns error when multiple token sources provided - arg and file", func(t *testing.T) {
		t.Setenv(EnvVarToken, "")

		err := CheckSingleTokenSource(secret.FromString("some-token-arg"), "/path/to/token")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "you can only pass one token source")
	})

	t.Run("returns error when all three token sources provided", func(t *testing.T) {
		t.Setenv(EnvVarToken, "some-token")

		err := CheckSingleTokenSource(secret.FromString("some-token-arg"), "/path/to/token")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "you can only pass one token source")
	})
}

func TestGetJoinToken_EnvVar(t *testing.T) {
	testToken := "test-token-data"

	t.Run("reads token from K0S_TOKEN env var", func(t *testing.T) {
		t.Setenv(EnvVarToken, testToken)

		token, err := GetJoinToken(secret.String{}, "")
		require.NoError(t, err)
		assert.Equal(t, testToken, token)
	})

	t.Run("empty K0S_TOKEN returns empty string", func(t *testing.T) {
		t.Setenv(EnvVarToken, "")

		token, err := GetJoinToken(secret.String{}, "")
		require.NoError(t, err)
		assert.Empty(t, token)
	})
}

func TestGetJoinToken_TokenArg(t *testing.T) {
	testToken := "test-token-data"

	t.Run("reads token from argument", func(t *testing.T) {
		t.Setenv(EnvVarToken, "")

		token, err := GetJoinToken(secret.FromString("some-token-arg"), "")
		require.NoError(t, err)
		assert.Equal(t, testToken, token)
	})
}

func TestGetJoinToken_TokenFile(t *testing.T) {
	testToken := "test-token-from-file"

	t.Run("reads token from file", func(t *testing.T) {
		t.Setenv(EnvVarToken, "")

		tmpDir := t.TempDir()
		tokenFile := filepath.Join(tmpDir, "token")
		require.NoError(t, os.WriteFile(tokenFile, []byte(testToken), 0600))

		token, err := GetJoinToken(secret.String{}, tokenFile)
		require.NoError(t, err)
		assert.Equal(t, testToken, token)
	})

	t.Run("returns error for non-existent file", func(t *testing.T) {
		t.Setenv(EnvVarToken, "")

		_, err := GetJoinToken(secret.String{}, "/non/existent/path")
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

		_, err := GetJoinToken(secret.String{}, tokenFile)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "token file")
		assert.Contains(t, err.Error(), "is empty")
		assert.Contains(t, err.Error(), "k0s token create")
	})
}

func TestGetJoinToken_NoToken(t *testing.T) {
	t.Run("returns empty string when no token provided", func(t *testing.T) {
		t.Setenv(EnvVarToken, "")

		token, err := GetJoinToken(secret.String{}, "")
		require.NoError(t, err)
		assert.Empty(t, token)
	})
}
