// SPDX-FileCopyrightText: 2025 k0s authors
// SPDX-License-Identifier: Apache-2.0

package certificate

import (
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	cfsslconfig "github.com/cloudflare/cfssl/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnsureCA(t *testing.T) {
	rootDir := t.TempDir()

	// Create the CA
	certManager := NewManager(rootDir)
	_, err := certManager.EnsureCA("ca", t.Name(), 100000*time.Hour)
	require.NoError(t, err)

	pemBytes, _ := os.ReadFile(filepath.Join(rootDir, "ca.crt"))
	cert, err := parseCert(pemBytes)
	require.NoError(t, err)
	// check the expiration date of the cert
	assert.Equal(t, cert.NotBefore.Add(100000*time.Hour), cert.NotAfter)
}

func TestEnsureCertificate(t *testing.T) {
	rootDir := t.TempDir()

	// Create the CA
	certManager := NewManager(rootDir)
	ca, err := certManager.EnsureCA("ca", t.Name(), 100000*time.Hour)
	require.NoError(t, err)

	req := Request{
		Name: "test",
		CN:   "kubernetes-test",
		O:    "system:masters",
		CA:   ca,
	}
	certData, err := certManager.EnsureCertificate(req, 1, 10000*time.Hour)
	require.NoError(t, err)
	cert, err := parseCert(certData.Cert)
	require.NoError(t, err)

	// check the expiration date of the cert
	assert.Equal(t, cert.NotBefore.Add(10000*time.Hour), cert.NotAfter)
	// check if the cert has the `signing` usage
	assert.NotEqual(t, 0, cert.KeyUsage&x509.KeyUsageDigitalSignature)
	// check if the cert has the `key encipherment` usage
	assert.NotEqual(t, 0, cert.KeyUsage&x509.KeyUsageKeyEncipherment)
	assert.Equal(t,
		[]x509.ExtKeyUsage{cfsslconfig.ExtKeyUsage["server auth"], cfsslconfig.ExtKeyUsage["client auth"]},
		cert.ExtKeyUsage,
	)
}

func parseCert(pemBytes []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, errors.New("no PEM data found")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, err
	}
	return cert, nil
}
