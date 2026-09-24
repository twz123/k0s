// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"testing"
	"time"

	"github.com/k0sproject/k0s/internal/crypto/kdf"

	certutil "k8s.io/client-go/util/cert"
	"k8s.io/client-go/util/keyutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeriveKubeletServingCA(t *testing.T) {
	t.Run("matches golden values", func(t *testing.T) {
		// The kubelet-serving CA derived from a pinned cluster CA key. The
		// derivation is wire format: if these change, every kubelet in every
		// cluster gets a new kubelet-serving CA.
		const (
			scalar  = "61ea6b7b382506c0df930f4e0b428a2276c436c8c604f44e8c260fa23355745d"
			certSum = "3236fe8f0891d47113271e55da175d06ef07d3b92253ca19737f622b203ceec7"
		)

		clusterCAScalar, err := hex.DecodeString("0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20")
		require.NoError(t, err)
		clusterCAKey, err := ecdsa.ParseRawPrivateKey(elliptic.P256(), clusterCAScalar)
		require.NoError(t, err)
		notBefore := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
		clusterCACert := &x509.Certificate{NotBefore: notBefore, NotAfter: notBefore.AddDate(10, 0, 0)}
		material, err := kdf.FromPrivateKey(clusterCAKey)
		require.NoError(t, err)

		ca, err := DeriveKubeletServingCA(material, clusterCACert)
		require.NoError(t, err)
		derivedScalar, err := ca.Key.Bytes()
		require.NoError(t, err)
		assert.Equal(t, scalar, hex.EncodeToString(derivedScalar), "Derived key changed")
		derivedCertSum := sha256.Sum256(ca.Cert.Raw)
		assert.Equal(t, certSum, hex.EncodeToString(derivedCertSum[:]), "Derived certificate changed")
	})

	ca, clusterCACert := newTestKubeletServingCA(t)

	t.Run("is a self-signed server auth CA", func(t *testing.T) {
		assert.Equal(t, "CN=kubernetes-kubelet-serving-ca", ca.Cert.Subject.String())
		assert.Equal(t, ca.Cert.RawSubject, ca.Cert.RawIssuer, "Certificate isn't self-issued")
		assert.True(t, ca.Cert.IsCA, "Certificate isn't a CA")
		assert.Equal(t, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, ca.Cert.ExtKeyUsage, "CA should be restricted to server auth")
	})

	t.Run("follows the cluster CA validity", func(t *testing.T) {
		assert.True(t, ca.Cert.NotBefore.Equal(clusterCACert.NotBefore), "NotBefore differs from cluster CA")
		assert.True(t, ca.Cert.NotAfter.Equal(clusterCACert.NotAfter), "NotAfter differs from cluster CA")
	})

	t.Run("round-trips through PEM", func(t *testing.T) {
		keyPEM, err := ca.KeyPEM()
		require.NoError(t, err)
		key, err := keyutil.ParsePrivateKeyPEM(keyPEM)
		require.NoError(t, err)
		assert.True(t, ca.Key.Equal(key), "Key doesn't survive the PEM round trip")

		certs, err := certutil.ParseCertsPEM(ca.CertPEM())
		require.NoError(t, err)
		require.Len(t, certs, 1)
		assert.Equal(t, ca.Cert.Raw, certs[0].Raw, "Certificate doesn't survive the PEM round trip")
	})
}

// Generates a cluster CA and derives the kubelet-serving CA from it.
func newTestKubeletServingCA(t *testing.T) (*KubeletServingCA, *x509.Certificate) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	clusterCACert, err := certutil.NewSelfSignedCACert(certutil.Config{CommonName: "kubernetes-ca"}, key)
	require.NoError(t, err)
	material, err := kdf.FromPrivateKey(key)
	require.NoError(t, err)
	ca, err := DeriveKubeletServingCA(material, clusterCACert)
	require.NoError(t, err)
	return ca, clusterCACert
}

func TestKubeletServingTrustBundle(t *testing.T) {
	ca, clusterCACert := newTestKubeletServingCA(t)

	bundle := kubeletServingTrustBundle(ca, clusterCACert)

	certs, err := certutil.ParseCertsPEM(bundle)
	require.NoError(t, err)
	require.Len(t, certs, 2, "Bundle should hold exactly two certificates")
	assert.Equal(t, ca.Cert.Raw, certs[0].Raw, "Bundle should start with the kubelet-serving CA")
	assert.Equal(t, clusterCACert.Raw, certs[1].Raw, "Bundle should end with the cluster CA")
}
