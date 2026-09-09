// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/k0sproject/k0s/internal/crypto/kdf"
	"github.com/k0sproject/k0s/pkg/certificate"
	"github.com/k0sproject/k0s/pkg/component/controller"
	"github.com/k0sproject/k0s/pkg/config"

	certutil "k8s.io/client-go/util/cert"
	"k8s.io/client-go/util/keyutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeriveKubeletServingCA(t *testing.T) {
	k0sVars, err := config.NewCfgVars(nil, t.TempDir())
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(k0sVars.CertRootDir, 0755))
	certManager := certificate.Manager{K0sVars: k0sVars}
	require.NoError(t, certManager.EnsureCA("ca", "kubernetes-ca", 24*time.Hour))
	caKeyPEM, err := os.ReadFile(filepath.Join(k0sVars.CertRootDir, "ca.key"))
	require.NoError(t, err)
	caCertPEM, err := os.ReadFile(filepath.Join(k0sVars.CertRootDir, "ca.crt"))
	require.NoError(t, err)

	t.Run("rejects an invalid cluster CA key", func(t *testing.T) {
		_, _, err := deriveKubeletServingCA([]byte("not a key"), caCertPEM)
		assert.ErrorContains(t, err, "failed to parse cluster CA key")
	})

	t.Run("rejects an invalid cluster CA certificate", func(t *testing.T) {
		_, _, err := deriveKubeletServingCA(caKeyPEM, []byte("not a cert"))
		assert.ErrorContains(t, err, "failed to parse cluster CA certificate")

		_, _, err = deriveKubeletServingCA(caKeyPEM, append(append([]byte{}, caCertPEM...), caCertPEM...))
		assert.ErrorContains(t, err, "expected exactly one cluster CA certificate, found 2")
	})

	t.Run("derives from the cluster CA", func(t *testing.T) {
		caKey, err := keyutil.ParsePrivateKeyPEM(caKeyPEM)
		require.NoError(t, err)
		caCerts, err := certutil.ParseCertsPEM(caCertPEM)
		require.NoError(t, err)
		require.Len(t, caCerts, 1)
		material, err := kdf.FromPrivateKey(caKey)
		require.NoError(t, err)
		expected, err := controller.DeriveKubeletServingCA(material, caCerts[0])
		require.NoError(t, err)

		clusterCACert, ca, err := deriveKubeletServingCA(caKeyPEM, caCertPEM)
		require.NoError(t, err)
		assert.Equal(t, caCerts[0].Raw, clusterCACert.Raw, "Cluster CA certificate should be the parsed file")
		assert.True(t, expected.Key.Equal(ca.Key), "Key should be derived from the cluster CA key")
		assert.Equal(t, expected.Cert.Raw, ca.Cert.Raw, "Certificate should follow the cluster CA certificate")
	})
}
