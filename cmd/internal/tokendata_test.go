// SPDX-FileCopyrightText: 2025 k0s authors
// SPDX-License-Identifier: Apache-2.0

package internal

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/k0sproject/k0s/internal/secret"
	"github.com/k0sproject/k0s/pkg/token"

	bootstraptokenv1 "k8s.io/kubernetes/cmd/kubeadm/app/apis/bootstraptoken/v1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetJoinTokenSource_SingleSource(t *testing.T) {
	t.Run("no error when no token sources provided", func(t *testing.T) {
		t.Setenv(EnvVarToken, "")

		_, err := GetJoinTokenSource(secret.String{}, "")
		require.NoError(t, err)
	})

	t.Run("no error when only arg provided", func(t *testing.T) {
		t.Setenv(EnvVarToken, "")

		_, err := GetJoinTokenSource(secret.FromString("some-token-arg"), "")
		require.NoError(t, err)
	})

	t.Run("no error when only file provided", func(t *testing.T) {
		t.Setenv(EnvVarToken, "")

		_, err := GetJoinTokenSource(secret.String{}, "/path/to/token")
		require.NoError(t, err)
	})

	t.Run("no error when only env provided", func(t *testing.T) {
		t.Setenv(EnvVarToken, "some-token")

		_, err := GetJoinTokenSource(secret.String{}, "")
		require.NoError(t, err)
	})

	t.Run("fails on multiple token sources provided - env and arg", func(t *testing.T) {
		t.Setenv(EnvVarToken, "some-token")

		getter, err := GetJoinTokenSource(secret.FromString("some-token-arg"), "")
		assert.Nil(t, getter)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "you can only pass one token source")
		assert.Contains(t, err.Error(), EnvVarToken)
	})

	t.Run("fails on multiple token sources provided - env and file", func(t *testing.T) {
		t.Setenv(EnvVarToken, "some-token")

		getter, err := GetJoinTokenSource(secret.String{}, "/path/to/token")
		assert.Nil(t, getter)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "you can only pass one token source")
	})

	t.Run("fails on multiple token sources provided - arg and file", func(t *testing.T) {
		t.Setenv(EnvVarToken, "")

		getter, err := GetJoinTokenSource(secret.FromString("some-token-arg"), "/path/to/token")
		assert.Nil(t, getter)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "you can only pass one token source")
	})

	t.Run("fails on all three token sources provided", func(t *testing.T) {
		t.Setenv(EnvVarToken, "some-token")

		getter, err := GetJoinTokenSource(secret.FromString("some-token-arg"), "/path/to/token")
		assert.Nil(t, getter)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "you can only pass one token source")
	})
}

func TestGetJoinTokenSource_EnvVar(t *testing.T) {
	testToken := encodedTestToken(t)

	t.Run("reads token from K0S_TOKEN env var", func(t *testing.T) {
		t.Setenv(EnvVarToken, testToken)

		jt, err := getJoinToken(t, secret.String{}, "")
		require.NoError(t, err)
		assert.Equal(t, testToken, encoded(t, jt))
	})

	t.Run("fails for an undecodable token", func(t *testing.T) {
		t.Setenv(EnvVarToken, "not a token")

		_, err := getJoinToken(t, secret.String{}, "")
		assert.ErrorContains(t, err, "failed to decode join token from K0S_TOKEN")
	})

	t.Run("empty K0S_TOKEN counts as no token", func(t *testing.T) {
		t.Setenv(EnvVarToken, "")

		jt, err := getJoinToken(t, secret.String{}, "")
		require.NoError(t, err)
		assert.Zero(t, jt, "Expected no token")
	})
}

func TestGetJoinTokenSource_TokenArg(t *testing.T) {
	testToken := encodedTestToken(t)

	t.Run("reads token from argument", func(t *testing.T) {
		t.Setenv(EnvVarToken, "")

		jt, err := getJoinToken(t, secret.FromString(testToken), "")
		require.NoError(t, err)
		assert.Equal(t, testToken, encoded(t, jt))
	})

	t.Run("fails for an undecodable token", func(t *testing.T) {
		t.Setenv(EnvVarToken, "")

		_, err := getJoinToken(t, secret.FromString("not a token"), "")
		assert.ErrorContains(t, err, "failed to decode join token argument")
	})
}

func TestGetJoinTokenSource_TokenFile(t *testing.T) {
	testToken := encodedTestToken(t)

	t.Run("reads token from file", func(t *testing.T) {
		t.Setenv(EnvVarToken, "")

		tokenFile := filepath.Join(t.TempDir(), "token")
		require.NoError(t, os.WriteFile(tokenFile, []byte(testToken), 0600))

		jt, err := getJoinToken(t, secret.String{}, tokenFile)
		require.NoError(t, err)
		assert.Equal(t, testToken, encoded(t, jt))
	})

	t.Run("reads the file when the token is requested", func(t *testing.T) {
		t.Setenv(EnvVarToken, "")

		tokenFile := filepath.Join(t.TempDir(), "token")
		getter, err := GetJoinTokenSource(secret.String{}, tokenFile)
		require.NoError(t, err)

		require.NoError(t, os.WriteFile(tokenFile, []byte(testToken), 0600))
		jt, err := getter()
		require.NoError(t, err)
		assert.Equal(t, testToken, encoded(t, jt))
	})

	t.Run("fails for an undecodable token", func(t *testing.T) {
		t.Setenv(EnvVarToken, "")

		tokenFile := filepath.Join(t.TempDir(), "token")
		require.NoError(t, os.WriteFile(tokenFile, []byte("not a token"), 0600))

		_, err := getJoinToken(t, secret.String{}, tokenFile)
		assert.ErrorContains(t, err, "failed to decode join token from "+tokenFile)
	})

	t.Run("returns error for non-existent file", func(t *testing.T) {
		t.Setenv(EnvVarToken, "")

		_, err := getJoinToken(t, secret.String{}, "/non/existent/path")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "token file")
		assert.Contains(t, err.Error(), "not found")
		assert.Contains(t, err.Error(), "k0s token create")
	})

	t.Run("returns error for empty file", func(t *testing.T) {
		t.Setenv(EnvVarToken, "")

		tokenFile := filepath.Join(t.TempDir(), "empty-token")
		require.NoError(t, os.WriteFile(tokenFile, []byte{}, 0600))

		_, err := getJoinToken(t, secret.String{}, tokenFile)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "token file")
		assert.Contains(t, err.Error(), "is empty")
		assert.Contains(t, err.Error(), "k0s token create")
	})
}

func TestGetJoinTokenSource_NoToken(t *testing.T) {
	t.Run("yields no token when no token provided", func(t *testing.T) {
		t.Setenv(EnvVarToken, "")

		jt, err := getJoinToken(t, secret.String{}, "")
		require.NoError(t, err)
		assert.Zero(t, jt, "Expected no token")
	})
}

// Determines the join token source, which is expected to be valid, and gets
// the token from it.
func getJoinToken(t *testing.T, tokenArg secret.String, tokenFile string) (token.JoinToken, error) {
	t.Helper()
	getter, err := GetJoinTokenSource(tokenArg, tokenFile)
	require.NoError(t, err)
	return getter()
}

// A join token for tests, in its encoded form.
func encodedTestToken(t *testing.T) string {
	t.Helper()
	tok := bootstraptokenv1.BootstrapTokenString{ID: "abcdef", Secret: "0123456789abcdef"}
	jt, err := token.GenerateJoinToken("https://example.com", []byte("the cert"), token.WorkerTokenAuthName, &tok)
	require.NoError(t, err)
	return encoded(t, jt)
}

// The encoded form of the given token, which is expected to be there.
func encoded(t *testing.T, jt token.JoinToken) string {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, jt.Encode(&buf))
	return buf.String()
}
