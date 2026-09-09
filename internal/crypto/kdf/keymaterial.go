// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package kdf

import (
	"crypto"
	"crypto/hkdf"
	"crypto/sha256"
	"crypto/x509"
	"errors"

	"github.com/k0sproject/k0s/internal/secret"
)

// KeyMaterial is secret input keying material for key derivations.
type KeyMaterial struct {
	secret.Value[KeyMaterial, []byte]
}

// FromPrivateKey returns the secret bytes of the given private key as key
// material. These are the key's PKCS #8 encoding, which is the same for any
// encoding the key was parsed from, be it PKCS #1, PKCS #8 or SEC 1, with or
// without additional PEM blocks, comments or differing line endings. For RSA
// keys, the order of the primes and the private exponent are taken as they
// were parsed, so a key that has been re-encoded by another tool in a
// mathematically equivalent but different way yields different material.
func FromPrivateKey(key crypto.PrivateKey) (KeyMaterial, error) {
	bytes, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return KeyMaterial{}, err
	}
	return KeyMaterial{secret.Of[KeyMaterial](bytes)}, nil
}

// Derive binds the key material to the given purpose, which should be a
// URI-like string that is unique to the keys' use, such as
// "k0sproject.io/some-ca".
func (m KeyMaterial) Derive(purpose string) (DerivedKey, error) {
	material, err := m.Reveal()
	if err != nil {
		return DerivedKey{}, err
	}
	if purpose == "" {
		return DerivedKey{}, errors.New("no purpose")
	}

	prk, err := hkdf.Extract(sha256.New, material, []byte(purpose))
	if err != nil {
		return DerivedKey{}, err
	}
	return DerivedKey{secret.Of[DerivedKey](prk)}, nil
}
