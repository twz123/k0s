// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package token_test

import (
	"fmt"
	"testing"

	"github.com/k0sproject/k0s/pkg/token"

	bootstraptokenv1 "k8s.io/kubernetes/cmd/kubeadm/app/apis/bootstraptoken/v1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestJoinToken_Zero(t *testing.T) {
	t.Parallel()

	var underTest token.JoinToken

	t.Run("holds no bootstrap kubeconfig", func(t *testing.T) {
		kubeconfig, err := underTest.BootstrapKubeconfig()
		assert.Nil(t, kubeconfig)
		assert.Equal(t, token.NoJoinTokenError{}, err)
	})

	t.Run("makes no join client", func(t *testing.T) {
		client, err := underTest.NewJoinClient()
		assert.Nil(t, client)
		assert.Equal(t, token.NoJoinTokenError{}, err)
	})
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
