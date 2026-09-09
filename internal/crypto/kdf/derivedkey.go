// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package kdf

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/sha256"
	"fmt"
	"strconv"

	"github.com/k0sproject/k0s/internal/secret"
)

// The HKDF info strings for the keys that can be obtained from a DerivedKey.
const (
	derivedKeyInfoP256 = "signing-key/v1/ecdsa-p256/"

	// The number of candidates to try before giving up. Every candidate is
	// rejected with a probability of about 2^-32, so exhausting this many is
	// a sure sign of a bug, not of bad luck.
	derivedKeyMaxCandidates = 16
)

// DerivedKey is key material that has been bound to a purpose, see
// [KeyMaterial.Derive]. Keys of various kinds can be obtained from it, each
// kind unrelated to the others.
type DerivedKey struct {
	secret.Value[DerivedKey, []byte]
}

// P256Key returns the P-256 private key for this derived key.
//
// This is key pair generation by rejection sampling, as in FIPS 186-5,
// Appendix A.2.2 (formerly FIPS 186-4, Appendix B.4.2), with the candidates
// taken from HKDF instead of a random bit generator. Candidates that don't
// yield a valid scalar are skipped by incrementing a counter in the info
// string. It differs from FIPS in that candidates are used as they are,
// rejecting zero along with those not below the curve order, rather than
// adding one to candidates not above the order minus two. Either way, the
// accepted scalars are uniformly distributed over the valid range. This
// borrows the rejection rule to avoid modulo bias; it is not an approved key
// generation procedure in the FIPS sense, since the candidates don't come from
// an approved random bit generator. Seeding [ecdsa.GenerateKey] is not an
// option: its output doesn't deterministically depend on the random source,
// and newer Go versions ignore the source altogether.
func (k DerivedKey) P256Key() (*ecdsa.PrivateKey, error) {
	return k.p256Key(func(candidate []byte) (*ecdsa.PrivateKey, error) {
		return ecdsa.ParseRawPrivateKey(elliptic.P256(), candidate)
	})
}

// Implements P256Key. The parse function turns 32 candidate bytes into a key,
// or rejects them.
func (k DerivedKey) p256Key(parse func(candidate []byte) (*ecdsa.PrivateKey, error)) (*ecdsa.PrivateKey, error) {
	prk, err := k.Reveal()
	if err != nil {
		return nil, err
	}

	scalarLen := (elliptic.P256().Params().N.BitLen() + 7) / 8
	var rejected error
	for i := range derivedKeyMaxCandidates {
		candidate, err := hkdf.Expand(sha256.New, prk, derivedKeyInfoP256+strconv.Itoa(i), scalarLen)
		if err != nil {
			return nil, err
		}

		// The curve and the candidate length are fixed, so the only way for
		// a candidate to be rejected is being zero or not below the curve
		// order. The error isn't inspected any further, since its exact
		// shape differs between Go versions.
		key, err := parse(candidate)
		if err == nil {
			return key, nil
		}
		rejected = err
	}

	return nil, NoCandidateError{rejected}
}

// NoCandidateError is the error returned when none of the candidates for a key
// was acceptable. Since every candidate is rejected with a probability of
// about 2^-32, this is a sure sign of a bug, not of bad luck.
type NoCandidateError struct {
	// Rejected is the reason why the last candidate was rejected.
	Rejected error
}

// Error implements [error].
func (e NoCandidateError) Error() string {
	return fmt.Sprintf("no acceptable candidate among %d: %v", derivedKeyMaxCandidates, e.Rejected)
}

// Unwrap returns the reason why the last candidate was rejected.
func (e NoCandidateError) Unwrap() error { return e.Rejected }
