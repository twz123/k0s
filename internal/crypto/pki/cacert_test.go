// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package pki

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewSelfSignedCACert(t *testing.T) {
	const commonName = "test-ca"
	extKeyUsage := []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	notBefore := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	notAfter := notBefore.AddDate(10, 0, 0)
	now := notBefore.Add(1 * time.Hour)

	key := testKey(t)
	cert, err := NewSelfSignedCACert(key, commonName, extKeyUsage, notBefore, notAfter)
	require.NoError(t, err)

	t.Run("matches golden value", func(t *testing.T) {
		// The certificate template is wire format: if this changes, every
		// certificate created this way changes. The key is pinned, so the
		// value is stable.
		const certSum = "3101d4509989d91544a782811b7b0fed00eeb764938a1a159a85f98501173899"
		sum := sha256.Sum256(cert.Raw)
		assert.Equal(t, certSum, hex.EncodeToString(sum[:]), "Certificate changed")
	})

	t.Run("is deterministic", func(t *testing.T) {
		again, err := NewSelfSignedCACert(key, commonName, extKeyUsage, notBefore, notAfter)
		require.NoError(t, err)
		assert.Equal(t, cert.Raw, again.Raw, "Certificates differ between invocations")
	})

	t.Run("is a self-signed CA", func(t *testing.T) {
		assert.Equal(t, "CN="+commonName, cert.Subject.String())
		assert.Equal(t, cert.RawSubject, cert.RawIssuer, "Certificate isn't self-issued")
		assert.NoError(t, cert.CheckSignatureFrom(cert), "Certificate isn't self-signed")
		assert.Empty(t, cert.AuthorityKeyId, "Self-signed certificates don't carry an authority key ID")
		assert.NotEmpty(t, cert.SubjectKeyId, "Subject key ID should be derived from the public key")

		assert.True(t, cert.IsCA, "Certificate isn't a CA")
		assert.True(t, cert.BasicConstraintsValid, "Basic constraints should be present")
		assert.True(t, cert.MaxPathLenZero, "CA should not be allowed to issue intermediate CAs")
		assert.Zero(t, cert.MaxPathLen, "CA should not be allowed to issue intermediate CAs")
		assert.Equal(t, x509.KeyUsageCertSign|x509.KeyUsageCRLSign, cert.KeyUsage, "CA should be restricted to signing certificates and CRLs")
		assert.Equal(t, extKeyUsage, cert.ExtKeyUsage, "CA should carry the given extended key usages")

		assert.Equal(t, x509.ECDSAWithSHA256, cert.SignatureAlgorithm, "Certificate should be signed with ECDSA and SHA-256")
		assert.Equal(t, x509.ECDSA, cert.PublicKeyAlgorithm, "Public key should be ECDSA")
		if pub, ok := cert.PublicKey.(*ecdsa.PublicKey); assert.True(t, ok, "Public key isn't ECDSA") {
			assert.True(t, pub.Equal(&key.PublicKey), "Certificate doesn't belong to the key")
		}

		assert.True(t, cert.NotBefore.Equal(notBefore), "Unexpected NotBefore")
		assert.True(t, cert.NotAfter.Equal(notAfter), "Unexpected NotAfter")
		assert.Positive(t, cert.SerialNumber.Sign(), "Serial number must be positive")
		assert.LessOrEqual(t, len(cert.SerialNumber.Bytes()), 16, "Serial number must fit in 16 octets")
	})

	leaf := issueTestCert(t, key, cert, x509.ExtKeyUsageServerAuth)

	t.Run("verifies issued certs", func(t *testing.T) {
		_, err := leaf.Verify(x509.VerifyOptions{
			Roots:       poolOf(cert),
			DNSName:     "worker-0",
			CurrentTime: now,
		})
		assert.NoError(t, err, "Issued cert should verify against the CA")
	})

	t.Run("restricts extended key usages", func(t *testing.T) {
		clientCert := issueTestCert(t, key, cert, x509.ExtKeyUsageClientAuth)
		_, err := clientCert.Verify(x509.VerifyOptions{
			Roots:       poolOf(cert),
			KeyUsages:   []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
			CurrentTime: now,
		})
		var invalidErr x509.CertificateInvalidError
		if assert.ErrorAs(t, err, &invalidErr, "Client cert should not verify for client auth") {
			assert.Equal(t, x509.IncompatibleUsage, invalidErr.Reason)
		}
	})

	t.Run("accepts any cert for the same key", func(t *testing.T) {
		variant, err := NewSelfSignedCACert(key, commonName, extKeyUsage, notBefore.Add(-1*time.Hour), notAfter)
		require.NoError(t, err)
		require.NotEqual(t, cert.Raw, variant.Raw, "Test needs a different certificate")

		_, err = leaf.Verify(x509.VerifyOptions{
			Roots:       poolOf(variant),
			DNSName:     "worker-0",
			CurrentTime: now,
		})
		assert.NoError(t, err, "Issued cert should verify against a different certificate for the same key")
	})

	t.Run("rejects a missing key", func(t *testing.T) {
		_, err := NewSelfSignedCACert(nil, commonName, extKeyUsage, notBefore, notAfter)
		assert.ErrorContains(t, err, "no key")
	})

	t.Run("rejects an empty common name", func(t *testing.T) {
		_, err := NewSelfSignedCACert(key, "", extKeyUsage, notBefore, notAfter)
		assert.ErrorContains(t, err, "no common name")
	})
}

// Returns the pinned P-256 key. Its scalar is fixed, so that golden values can
// be pinned against it.
func testKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	scalar, err := hex.DecodeString("0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20")
	require.NoError(t, err)
	key, err := ecdsa.ParseRawPrivateKey(elliptic.P256(), scalar)
	require.NoError(t, err)
	return key
}

// Issues a kubelet-style certificate for worker-0 with the given extended key usage.
func issueTestCert(t *testing.T, caKey *ecdsa.PrivateKey, caCert *x509.Certificate, usage x509.ExtKeyUsage) *x509.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName:   "system:node:worker-0",
			Organization: []string{"system:nodes"},
		},
		DNSNames:    []string{"worker-0"},
		NotBefore:   caCert.NotBefore,
		NotAfter:    caCert.NotBefore.Add(365 * 24 * time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{usage},
	}

	der, err := x509.CreateCertificate(rand.Reader, template, caCert, &key.PublicKey, caKey)
	require.NoError(t, err)
	cert, err := x509.ParseCertificate(der)
	require.NoError(t, err)
	return cert
}

func poolOf(certs ...*x509.Certificate) *x509.CertPool {
	pool := x509.NewCertPool()
	for _, cert := range certs {
		pool.AddCert(cert)
	}
	return pool
}
