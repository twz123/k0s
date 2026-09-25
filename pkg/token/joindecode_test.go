// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package token_test

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/k0sproject/k0s/internal/secret"
	"github.com/k0sproject/k0s/pkg/token"

	bootstraptokenv1 "k8s.io/kubernetes/cmd/kubeadm/app/apis/bootstraptoken/v1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJoinToken_RoundTrip(t *testing.T) {
	t.Parallel()

	kubeconfig := []byte("the-payload")
	var encoded bytes.Buffer
	require.NoError(t, token.FromKubeconfigBytes(kubeconfig).Encode(&encoded))

	decoded, err := token.DecodeJoinToken(secret.FromBytes(encoded.Bytes()))
	require.NoError(t, err)
	revealed, err := decoded.RevealKubeconfig()
	require.NoError(t, err)
	assert.Equal(t, kubeconfig, revealed)
}

func TestJoinToken_RevealKubeconfig_NoToken(t *testing.T) {
	t.Parallel()

	var underTest token.JoinToken
	encoded, err := underTest.RevealKubeconfig()
	assert.Equal(t, secret.NoValueError[token.JoinToken]{}, err)
	assert.Empty(t, encoded)
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

func TestJoinToken_Type(t *testing.T) {
	t.Parallel()

	tok := bootstraptokenv1.BootstrapTokenString{ID: "abcdef", Secret: "0123456789abcdef"}
	controllerToken, err := token.GenerateJoinToken("https://example.com", []byte("the cert"), token.ControllerTokenAuthName, &tok)
	require.NoError(t, err)
	workerToken, err := token.GenerateJoinToken("https://example.com", []byte("the cert"), token.WorkerTokenAuthName, &tok)
	require.NoError(t, err)

	t.Run("bootstraps kubelets from worker tokens only", func(t *testing.T) {
		_, err := workerToken.BootstrapKubeconfig()
		assert.NoError(t, err)

		_, err = controllerToken.BootstrapKubeconfig()
		assert.ErrorContains(t, err, "wrong token type controller-bootstrap, expected type: kubelet-bootstrap")
	})

	t.Run("joins controllers from controller tokens only", func(t *testing.T) {
		_, err := workerToken.NewJoinClient()
		assert.ErrorContains(t, err, "wrong token type kubelet-bootstrap, expected type: controller-bootstrap")
	})

	t.Run("is redacted when formatted", func(t *testing.T) {
		assert.Equal(t, "<token.JoinToken>", fmt.Sprintf("%v", workerToken))
		assert.Equal(t, "<token.JoinToken>", workerToken.String())
	})
}
