// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package kdf

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDerivedKey_P256Key(t *testing.T) {
	const purpose = "k0sproject.io/test"

	t.Run("matches golden values", func(t *testing.T) {
		// The derivation is wire format: if this changes, every derived key
		// changes. The EC key is pinned, so the value is stable. The RSA path
		// only differs in the PKCS #8 encoding of the key, which is Go's, so
		// it isn't pinned.
		const scalar = "b4590275e75c97f5f02043a2a2363be08556371f826f76427025f5170f64fa2a"

		derived, err := testKeyMaterial(t, testECKey(t)).Derive(purpose)
		require.NoError(t, err)
		key, err := derived.P256Key()
		require.NoError(t, err)
		derivedScalar, err := key.Bytes()
		require.NoError(t, err)
		assert.Equal(t, scalar, hex.EncodeToString(derivedScalar), "Derived key changed")
	})

	for _, test := range testKeys(t) {
		t.Run(test.name, func(t *testing.T) {
			derived, err := testKeyMaterial(t, test.key).Derive(purpose)
			require.NoError(t, err)
			key, err := derived.P256Key()
			require.NoError(t, err)

			t.Run("is deterministic", func(t *testing.T) {
				again, err := derived.P256Key()
				require.NoError(t, err)
				assert.True(t, key.Equal(again), "Keys differ between derivations")
			})

			t.Run("is on P-256", func(t *testing.T) {
				assert.Equal(t, elliptic.P256(), key.Curve)
			})
		})
	}

	t.Run("differs between purposes", func(t *testing.T) {
		material := testKeyMaterial(t, testECKey(t))
		derived, err := material.Derive(purpose)
		require.NoError(t, err)
		key, err := derived.P256Key()
		require.NoError(t, err)
		other, err := material.Derive("k0sproject.io/other")
		require.NoError(t, err)
		otherKey, err := other.P256Key()
		require.NoError(t, err)
		assert.False(t, key.Equal(otherKey), "Different purposes should yield different keys")
	})

	t.Run("rejects a missing key", func(t *testing.T) {
		_, err := DerivedKey{}.P256Key()
		assert.ErrorContains(t, err, "no kdf.DerivedKey")
	})

	t.Run("skips rejected candidates", func(t *testing.T) {
		derived, err := testKeyMaterial(t, testECKey(t)).Derive(purpose)
		require.NoError(t, err)

		var candidates [][]byte
		key, err := derived.p256Key(func(candidate []byte) (*ecdsa.PrivateKey, error) {
			candidates = append(candidates, candidate)
			if len(candidates) < 3 {
				return nil, errors.New("rejected")
			}
			return ecdsa.ParseRawPrivateKey(elliptic.P256(), candidate)
		})
		require.NoError(t, err)
		require.Len(t, candidates, 3, "Expected the third candidate to be accepted")
		assert.NotEqual(t, candidates[0], candidates[1], "Candidates should differ")
		assert.NotEqual(t, candidates[1], candidates[2], "Candidates should differ")

		scalar, err := key.Bytes()
		require.NoError(t, err)
		assert.Equal(t, candidates[2], scalar, "Key should be the accepted candidate")
	})

	t.Run("gives up eventually", func(t *testing.T) {
		derived, err := testKeyMaterial(t, testECKey(t)).Derive(purpose)
		require.NoError(t, err)

		calls := 0
		rejected := errors.New("rejected")
		_, err = derived.p256Key(func([]byte) (*ecdsa.PrivateKey, error) {
			calls++
			return nil, rejected
		})
		var noCandidate NoCandidateError
		if assert.ErrorAs(t, err, &noCandidate) {
			assert.Equal(t, rejected, noCandidate.Rejected, "Error should carry the last rejection")
		}
		assert.ErrorIs(t, err, rejected, "Error should unwrap to the last rejection")
		assert.EqualError(t, err, "no acceptable candidate among 16: rejected")
		assert.Equal(t, derivedKeyMaxCandidates, calls, "Should try the maximum number of candidates")
	})
}
