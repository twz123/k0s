// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package pki

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"math/big"
	"time"
)

// NewSelfSignedCACert creates a self-signed CA certificate for the given key,
// with the given subject common name, extended key usages and validity
// period. The result is byte-identical for identical inputs.
//
// The certificate's signature is a deterministic ECDSA signature according to
// RFC 6979, and the serial number and subject key identifier are derived from
// the inputs, so that every controller that holds the same key ends up with
// the same certificate. Trust bundles assembled from several controllers are
// thus free of duplicates. Byte identity is a nicety, not a requirement: X.509
// chain building matches certificates by subject and public key, so any
// certificate for the key is a valid trust anchor.
//
// Verifiers apply extended key usages along the whole chain, so the given
// usages restrict what the certificates issued by the CA can be used for. The
// CA is meant to issue leaf certificates only, so it forbids intermediates.
//
// The certificate template is wire format: changing it changes the certificate
// of every CA created this way.
func NewSelfSignedCACert(key *ecdsa.PrivateKey, commonName string, extKeyUsage []x509.ExtKeyUsage, notBefore, notAfter time.Time) (*x509.Certificate, error) {
	if key == nil {
		return nil, errors.New("no key")
	}
	if commonName == "" {
		return nil, errors.New("no common name")
	}

	serial, err := caCertSerial(&key.PublicKey, notBefore, notAfter)
	if err != nil {
		return nil, err
	}
	subjectKeyID, err := caCertSubjectKeyID(&key.PublicKey)
	if err != nil {
		return nil, err
	}

	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		ExtKeyUsage:           extKeyUsage,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
		SubjectKeyId:          subjectKeyID,
	}

	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, deterministicSigner{key})
	if err != nil {
		return nil, err
	}

	return x509.ParseCertificate(der)
}

// Computes the subject key identifier for the given public key, using method 1
// of RFC 7093, Section 2: the leftmost 160 bits of the SHA-256 hash of the
// public key's bit string. This is what Go does by default for CA certificates
// without a subject key identifier, but the default can be changed via
// GODEBUG, which would spoil the byte identity of the certificate.
func caCertSubjectKeyID(pub *ecdsa.PublicKey) ([]byte, error) {
	pubBytes, err := pub.Bytes()
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(pubBytes)
	return sum[:20], nil
}

// Computes a serial number that is unique for the given public key and
// validity period. It is positive and fits into 16 octets, well within the
// 20-octet limit of RFC 5280, Section 4.1.2.2.
func caCertSerial(pub *ecdsa.PublicKey, notBefore, notAfter time.Time) (*big.Int, error) {
	spki, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return nil, err
	}

	h := sha256.New()
	h.Write(spki)
	h.Write([]byte(notBefore.UTC().Format(time.RFC3339)))
	h.Write([]byte(notAfter.UTC().Format(time.RFC3339)))
	sum := h.Sum(nil)[:16]
	sum[0] &= 0x7f // ensure that the number is positive
	serial := new(big.Int).SetBytes(sum)
	if serial.Sign() == 0 {
		serial.SetInt64(1) // zero is not a valid serial number
	}

	return serial, nil
}

// A signer that produces deterministic ECDSA signatures according to RFC 6979,
// disregarding the random source it's being handed.
type deterministicSigner struct{ key *ecdsa.PrivateKey }

var _ crypto.Signer = deterministicSigner{}

// Public implements [crypto.Signer].
func (s deterministicSigner) Public() crypto.PublicKey { return s.key.Public() }

// Sign implements [crypto.Signer].
func (s deterministicSigner) Sign(_ io.Reader, digest []byte, opts crypto.SignerOpts) ([]byte, error) {
	// A nil random source requests an RFC 6979 signature.
	return s.key.Sign(nil, digest, opts)
}
