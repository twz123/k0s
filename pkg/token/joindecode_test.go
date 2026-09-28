// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package token_test

import (
	"bytes"
	"io"
	"testing"

	"github.com/k0sproject/k0s/internal/secret"
	"github.com/k0sproject/k0s/pkg/token"

	bootstraptokenv1 "k8s.io/kubernetes/cmd/kubeadm/app/apis/bootstraptoken/v1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJoinToken_RoundTrip(t *testing.T) {
	t.Parallel()

	tok := bootstraptokenv1.BootstrapTokenString{ID: "abcdef", Secret: "0123456789abcdef"}
	original, err := token.GenerateJoinToken("https://example.com", []byte("the cert"), token.WorkerTokenAuthName, &tok)
	require.NoError(t, err)

	var encoded bytes.Buffer
	require.NoError(t, original.Encode(&encoded))
	decoded, err := token.DecodeJoinToken(secret.FromBytes(encoded.Bytes()))
	require.NoError(t, err)

	expected, err := original.BootstrapKubeconfig()
	require.NoError(t, err)
	actual, err := decoded.BootstrapKubeconfig()
	require.NoError(t, err)
	assert.Equal(t, expected, actual)
}

func TestJoinToken_Encode_NoToken(t *testing.T) {
	t.Parallel()

	var underTest token.JoinToken
	assert.Equal(t, token.NoJoinTokenError{}, underTest.Encode(io.Discard))
}

func TestDecodeJoinToken_NoBytes(t *testing.T) {
	t.Parallel()

	decoded, err := token.DecodeJoinToken(secret.Bytes{})
	assert.Equal(t, secret.NoBytesError{}, err)
	assert.Zero(t, decoded, "Expected no token")
}

func TestDecodeJoinToken_InvalidBase64(t *testing.T) {
	t.Parallel()

	decoded, err := token.DecodeJoinToken(secret.FromBytes([]byte("not-valid-base64!!!")))
	assert.ErrorContains(t, err, "illegal base64 data")
	assert.Zero(t, decoded, "Expected no token")
}

func TestDecodeJoinToken_InvalidGzip(t *testing.T) {
	t.Parallel()

	// Valid base64, but not gzip data underneath.
	decoded, err := token.DecodeJoinToken(secret.FromBytes([]byte("bm90LWd6aXA=")))
	assert.ErrorContains(t, err, "unexpected EOF")
	assert.Zero(t, decoded, "Expected no token")
}
