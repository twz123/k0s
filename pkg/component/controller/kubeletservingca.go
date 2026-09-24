// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/k0sproject/k0s/internal/crypto/kdf"
	"github.com/k0sproject/k0s/internal/crypto/pki"
	"github.com/k0sproject/k0s/pkg/applier"
	"github.com/k0sproject/k0s/pkg/component/manager"
	"github.com/k0sproject/k0s/pkg/kubernetes"

	certificatesv1 "k8s.io/api/certificates/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	certutil "k8s.io/client-go/util/cert"
	"k8s.io/client-go/util/keyutil"

	"github.com/sirupsen/logrus"
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

// The file in the run directory that holds the trust bundle for kubelet
// serving certificates.
const kubeletServingCABundleFile = "kubelet-serving-ca-bundle.crt"

// Assembles the PEM-encoded trust bundle for kubelet serving certificates: the
// kubelet-serving CA certificate, followed by the cluster CA certificate, so
// that kubelet serving certificates issued by the cluster CA remain trusted.
// The cluster CA certificate is re-encoded, so that the bundle doesn't depend
// on how the cluster CA certificate file is formatted.
func kubeletServingTrustBundle(ca *KubeletServingCA, clusterCACert *x509.Certificate) []byte {
	return slices.Concat(ca.CertPEM(), pem.EncodeToMemory(&pem.Block{
		Type: certutil.CertificateBlockType, Bytes: clusterCACert.Raw,
	}))
}

// KubeletServingCAPublisher publishes the trust bundle for kubelet serving
// certificates in the cluster, so that workloads can verify the certificates
// that kubelets serve. The bundle is published as the kubelet-serving-ca.crt
// ConfigMap in the kube-system namespace, in the same shape as the
// kube-root-ca.crt ConfigMap that holds the cluster CA. If the API server
// serves ClusterTrustBundles, it is also published as a ClusterTrustBundle for
// the kubernetes.io/kubelet-serving signer, which can be mounted in any
// namespace.
type KubeletServingCAPublisher struct {
	// The kubelet-serving CA and the cluster CA certificate, which make up the
	// trust bundle for kubelet serving certificates.
	KubeletServingCA *KubeletServingCA
	ClusterCACert    *x509.Certificate
	Clients          kubernetes.ClientFactoryInterface

	log  logrus.FieldLogger
	stop context.CancelFunc
	done <-chan struct{}
}

var _ manager.Component = (*KubeletServingCAPublisher)(nil)

const kubeletServingCAStackName = "kubelet-serving-ca"

// Init implements [manager.Component].
func (p *KubeletServingCAPublisher) Init(context.Context) error {
	p.log = logrus.WithField("component", "kubelet-serving-ca")
	return nil
}

// Start implements [manager.Component].
func (p *KubeletServingCAPublisher) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	p.stop, p.done = cancel, done

	go func() {
		defer close(done)
		p.publish(ctx)
	}()

	return nil
}

// Stop implements [manager.Component].
func (p *KubeletServingCAPublisher) Stop() error {
	if p.stop != nil {
		p.stop()
		<-p.done
	}
	return nil
}

// Publishes the trust bundle, retrying until it succeeds or the context is done.
func (p *KubeletServingCAPublisher) publish(ctx context.Context) {
	for {
		clusterTrustBundle, err := p.tryPublish(ctx)
		if err == nil {
			p.log.WithField("clusterTrustBundle", clusterTrustBundle).Info("Published the kubelet-serving CA trust bundle")
			return
		}

		if ctx.Err() != nil {
			return
		}

		p.log.WithError(err).Warn("Failed to publish the kubelet-serving CA trust bundle, retrying")
		select {
		case <-ctx.Done():
			return
		case <-time.After(10 * time.Second):
		}
	}
}

func (p *KubeletServingCAPublisher) tryPublish(ctx context.Context) (clusterTrustBundle bool, _ error) {
	discoveryClient, err := p.Clients.GetDiscoveryClient()
	if err != nil {
		return false, err
	}
	clusterTrustBundle, err = servesClusterTrustBundles(ctx, discoveryClient)
	if err != nil {
		return false, err
	}

	resources, err := p.resources(clusterTrustBundle)
	if err != nil {
		return false, err
	}

	return clusterTrustBundle, applier.ApplyStack(ctx, p.Clients, resources, kubeletServingCAStackName)
}

// Determines whether the API server serves ClusterTrustBundles in the stable
// API version.
func servesClusterTrustBundles(ctx context.Context, client discovery.DiscoveryInterface) (bool, error) {
	resources, err := discovery.ToDiscoveryInterfaceWithContext(client).
		ServerResourcesForGroupVersionWithContext(ctx, "certificates.k8s.io/v1")
	if err != nil {
		// Cached clients have a sentinel error of their own.
		if apierrors.IsNotFound(err) || errors.Is(err, memory.ErrCacheNotFound) {
			return false, nil
		}
		return false, err
	}
	for _, resource := range resources.APIResources {
		if resource.Name == "clustertrustbundles" {
			return true, nil
		}
	}
	return false, nil
}

// Builds the resources to be published, optionally including a
// ClusterTrustBundle.
func (p *KubeletServingCAPublisher) resources(clusterTrustBundle bool) ([]*unstructured.Unstructured, error) {
	bundle := string(kubeletServingTrustBundle(p.KubeletServingCA, p.ClusterCACert))

	objects := []runtime.Object{
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "kubelet-serving-ca.crt",
				Namespace: metav1.NamespaceSystem,
			},
			Data: map[string]string{"ca.crt": bundle},
		},
	}

	if clusterTrustBundle {
		objects = append(objects, &certificatesv1.ClusterTrustBundle{
			ObjectMeta: metav1.ObjectMeta{Name: "kubernetes.io:kubelet-serving:k0s"},
			Spec: certificatesv1.ClusterTrustBundleSpec{
				SignerName:  "kubernetes.io/kubelet-serving",
				TrustBundle: bundle,
			},
		})
	}

	return applier.ToUnstructuredSlice(nil, objects...)
}
