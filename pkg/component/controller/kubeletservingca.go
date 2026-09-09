// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"

	"github.com/k0sproject/k0s/internal/crypto/kdf"
	"github.com/k0sproject/k0s/internal/crypto/pki"

	certutil "k8s.io/client-go/util/cert"
	"k8s.io/client-go/util/keyutil"
)

// What makes the kubelet-serving CA the kubelet-serving CA. These are wire
// format: changing any of them amounts to a CA rotation for every kubelet in
// every cluster.
const (
	kubeletServingCAPurpose    = "k0sproject.io/kubelet-serving-ca"
	kubeletServingCACommonName = "kubernetes-kubelet-serving-ca"
)

// KubeletServingCA is the certificate authority for the
// kubernetes.io/kubelet-serving signer.
//
// The CA is dedicated to that one signer, so that kubelet serving
// certificates are trusted by the parties that connect to kubelets, and by
// nothing else. In particular, they don't chain to the cluster CA, which every
// pod trusts as the Kubernetes API server. The CA is only ever meant to issue
// serving certificates, so it's restricted to server auth, which prevents any
// certificate issued by it from being used for client authentication.
//
// The CA is derived from the cluster CA, see [DeriveKubeletServingCA]. It must
// never be signed by the cluster CA, and must never be added to the cluster
// CA's certificate file. Either would let kubelet serving certificates chain
// to the cluster CA.
type KubeletServingCA struct {
	Key  *ecdsa.PrivateKey
	Cert *x509.Certificate
}

// DeriveKubeletServingCA derives the kubelet-serving CA from the given key
// material, which is meant to be the cluster CA key's. The CA certificate's
// validity period is the given cluster CA certificate's. This matters:
// verifiers check the validity of trust anchors, too, and a certificate
// minted on one controller is verified with the clocks of other nodes.
func DeriveKubeletServingCA(material kdf.KeyMaterial, clusterCACert *x509.Certificate) (*KubeletServingCA, error) {
	derived, err := material.Derive(kubeletServingCAPurpose)
	if err != nil {
		return nil, fmt.Errorf("failed to derive kubelet-serving CA key: %w", err)
	}
	key, err := derived.P256Key()
	if err != nil {
		return nil, fmt.Errorf("failed to derive kubelet-serving CA key: %w", err)
	}

	cert, err := pki.NewSelfSignedCACert(key, kubeletServingCACommonName,
		[]x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		clusterCACert.NotBefore, clusterCACert.NotAfter,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubelet-serving CA certificate: %w", err)
	}

	return &KubeletServingCA{Key: key, Cert: cert}, nil
}

// KeyPEM returns the PEM-encoded private key in SEC 1 form.
func (ca *KubeletServingCA) KeyPEM() ([]byte, error) {
	der, err := x509.MarshalECPrivateKey(ca.Key)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: keyutil.ECPrivateKeyBlockType, Bytes: der}), nil
}

// CertPEM returns the PEM-encoded CA certificate.
func (ca *KubeletServingCA) CertPEM() []byte {
	return pem.EncodeToMemory(&pem.Block{Type: certutil.CertificateBlockType, Bytes: ca.Cert.Raw})
}
