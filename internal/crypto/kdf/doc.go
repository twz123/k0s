// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

// Package kdf derives keys deterministically from existing key material.
//
// It exists so that every controller of a cluster can compute the same keys
// on its own, without any distribution mechanism: controllers already share
// the cluster CA key, and anything derived from it is the same everywhere.
//
// Whoever holds the key material can derive every key, so the material needs
// at least the protection of the most sensitive key derived from it. The
// converse doesn't hold: a derived key reveals neither the material nor the
// keys derived for other purposes.
//
// Derivations are done in two steps. [FromPrivateKey] turns a private key into
// [KeyMaterial], and [KeyMaterial.Derive] binds that material to a purpose,
// yielding a [DerivedKey]. Different purposes yield unrelated keys from the
// same material. Keys of a concrete kind are obtained from the derived key,
// such as a P-256 private key via [DerivedKey.P256Key]:
//
//	material, err := kdf.FromPrivateKey(clusterCAKey)
//	// ...
//	derived, err := material.Derive("k0sproject.io/some-ca")
//	// ...
//	key, err := derived.P256Key()
//
// Under the hood, this is HKDF-SHA256 (RFC 5869): binding to a purpose is the
// extract step, with the purpose as the salt, and obtaining a key is the expand
// step, with an info string per kind of key.
//
// Everything that goes into a derivation is wire format: the encoding of the
// key material, the purposes, the info strings and the way keys are obtained
// from HKDF output. Changing any of them amounts to a rotation of the affected
// keys in every cluster. Golden tests pin them.
//
// Key material and derived keys are [secret.Value]s, so that they never show
// up in logs or error messages by accident.
package kdf
