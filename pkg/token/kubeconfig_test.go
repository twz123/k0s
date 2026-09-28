// SPDX-FileCopyrightText: 2022 k0s authors
// SPDX-License-Identifier: Apache-2.0

package token

import (
	"testing"

	"k8s.io/client-go/tools/clientcmd"
	bootstraptokenv1 "k8s.io/kubernetes/cmd/kubeadm/app/apis/bootstraptoken/v1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateJoinToken(t *testing.T) {
	expected := `apiVersion: v1
clusters:
- cluster:
    certificate-authority-data: dGhlIGNlcnQ=
    server: the join URL
  name: k0s
contexts:
- context:
    cluster: k0s
    user: kubelet-bootstrap
  name: k0s
current-context: k0s
kind: Config
users:
- name: kubelet-bootstrap
  user:
    token: abcdef.0123456789abcdef
`

	tok := bootstraptokenv1.BootstrapTokenString{ID: "abcdef", Secret: "0123456789abcdef"}
	joinToken, err := GenerateJoinToken("the join URL", []byte("the cert"), "kubelet-bootstrap", &tok)
	require.NoError(t, err)
	kubeconfig, err := joinToken.BootstrapKubeconfig()
	require.NoError(t, err)
	roundtrip, err := clientcmd.Write(*kubeconfig)
	require.NoError(t, err)
	assert.Equal(t, expected, string(roundtrip))
}
