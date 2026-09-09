// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package kdf

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"strings"
	"sync"
	"testing"

	"k8s.io/client-go/util/keyutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFromPrivateKey(t *testing.T) {
	for _, test := range testKeys(t) {
		t.Run(test.name, func(t *testing.T) {
			material := testKeyMaterial(t, test.key)
			bytes, err := material.Reveal()
			require.NoError(t, err)
			require.NotEmpty(t, bytes)

			t.Run("is independent of the key encoding", func(t *testing.T) {
				keyPEM := nativeKeyPEM(t, test.key)
				pkcs8, err := x509.MarshalPKCS8PrivateKey(test.key)
				require.NoError(t, err)

				for name, keyPEM := range map[string][]byte{
					"native":  keyPEM,
					"PKCS #8": pem.EncodeToMemory(&pem.Block{Type: keyutil.PrivateKeyBlockType, Bytes: pkcs8}),
					"rewrapped": []byte("# A comment, and CRLF line endings\r\n" +
						strings.ReplaceAll(string(keyPEM), "\n", "\r\n")),
				} {
					t.Run(name, func(t *testing.T) {
						reparsed, err := keyutil.ParsePrivateKeyPEM(keyPEM)
						require.NoError(t, err)
						variant, err := FromPrivateKey(reparsed)
						require.NoError(t, err)
						variantBytes, err := variant.Reveal()
						require.NoError(t, err)
						assert.Equal(t, bytes, variantBytes, "Key material should not depend on the key's encoding")
					})
				}
			})
		})
	}

	t.Run("is secret", func(t *testing.T) {
		material := testKeyMaterial(t, testECKey(t))
		assert.Equal(t, "<kdf.KeyMaterial>", fmt.Sprint(material))
		assert.Equal(t, "%!d(kdf.KeyMaterial)", fmt.Sprintf("%d", material))
	})

	t.Run("differs between keys", func(t *testing.T) {
		var materials [][]byte
		for _, test := range testKeys(t) {
			bytes, err := testKeyMaterial(t, test.key).Reveal()
			require.NoError(t, err)
			materials = append(materials, bytes)
		}
		require.Len(t, materials, 2)
		assert.NotEqual(t, materials[0], materials[1], "Different keys should yield different material")
	})

	t.Run("rejects unsupported keys", func(t *testing.T) {
		_, err := FromPrivateKey("not a key")
		assert.Error(t, err)
	})
}

func TestKeyMaterial_Derive(t *testing.T) {
	const purpose = "k0sproject.io/test"

	t.Run("is deterministic", func(t *testing.T) {
		material := testKeyMaterial(t, testECKey(t))
		derived, err := material.Derive(purpose)
		require.NoError(t, err)
		again, err := material.Derive(purpose)
		require.NoError(t, err)
		assert.Equal(t, revealed(t, derived), revealed(t, again), "Derived keys differ between derivations")
	})

	t.Run("differs between purposes", func(t *testing.T) {
		material := testKeyMaterial(t, testECKey(t))
		derived, err := material.Derive(purpose)
		require.NoError(t, err)
		other, err := material.Derive("k0sproject.io/other")
		require.NoError(t, err)
		assert.NotEqual(t, revealed(t, derived), revealed(t, other), "Different purposes should yield different keys")
	})

	t.Run("differs between materials", func(t *testing.T) {
		var derived [][]byte
		for _, test := range testKeys(t) {
			key, err := testKeyMaterial(t, test.key).Derive(purpose)
			require.NoError(t, err)
			derived = append(derived, revealed(t, key))
		}
		require.Len(t, derived, 2)
		assert.NotEqual(t, derived[0], derived[1], "Different materials should yield different keys")
	})

	t.Run("is secret", func(t *testing.T) {
		derived, err := testKeyMaterial(t, testECKey(t)).Derive(purpose)
		require.NoError(t, err)
		assert.Equal(t, "<kdf.DerivedKey>", fmt.Sprint(derived))
		assert.Equal(t, "%!d(kdf.DerivedKey)", fmt.Sprintf("%d", derived))
	})

	t.Run("rejects missing material", func(t *testing.T) {
		_, err := KeyMaterial{}.Derive(purpose)
		assert.ErrorContains(t, err, "no kdf.KeyMaterial")
	})

	t.Run("rejects an empty purpose", func(t *testing.T) {
		_, err := testKeyMaterial(t, testECKey(t)).Derive("")
		assert.ErrorContains(t, err, "no purpose")
	})
}

// A private key for tests.
type testKey struct {
	name string
	key  crypto.PrivateKey
}

// Returns a pinned EC key and a freshly generated RSA key. The RSA key is
// generated once per test binary, since that takes a while. Deterministic RSA
// key generation is not an option: Go doesn't guarantee that a fixed random
// source yields the same key across releases.
func testKeys(t *testing.T) []testKey {
	t.Helper()
	rsaKey, err := testRSAKey()
	require.NoError(t, err)
	return []testKey{{"EC", testECKey(t)}, {"RSA", rsaKey}}
}

// Returns the pinned EC key. Its scalar is fixed, so that golden values can be
// pinned against it.
func testECKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	scalar, err := hex.DecodeString("0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20")
	require.NoError(t, err)
	key, err := ecdsa.ParseRawPrivateKey(elliptic.P256(), scalar)
	require.NoError(t, err)
	return key
}

var testRSAKey = sync.OnceValues(func() (*rsa.PrivateKey, error) {
	return rsa.GenerateKey(rand.Reader, 2048)
})

// Returns the key material of the given key.
func testKeyMaterial(t *testing.T, key crypto.PrivateKey) KeyMaterial {
	t.Helper()
	material, err := FromPrivateKey(key)
	require.NoError(t, err)
	return material
}

// Reveals the given derived key's bytes.
func revealed(t *testing.T, key DerivedKey) []byte {
	t.Helper()
	bytes, err := key.Reveal()
	require.NoError(t, err)
	return bytes
}

// Encodes the given key as PEM in its native format: PKCS #1 for RSA keys,
// SEC 1 for EC keys. This is the format that OpenSSL and cfssl write.
func nativeKeyPEM(t *testing.T, key crypto.PrivateKey) []byte {
	t.Helper()
	switch key := key.(type) {
	case *rsa.PrivateKey:
		return pem.EncodeToMemory(&pem.Block{Type: keyutil.RSAPrivateKeyBlockType, Bytes: x509.MarshalPKCS1PrivateKey(key)})
	case *ecdsa.PrivateKey:
		der, err := x509.MarshalECPrivateKey(key)
		require.NoError(t, err)
		return pem.EncodeToMemory(&pem.Block{Type: keyutil.ECPrivateKeyBlockType, Bytes: der})
	default:
		require.Failf(t, "Unsupported key type", "%T", key)
		return nil
	}
}
