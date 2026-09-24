// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/k0sproject/k0s/internal/crypto/kdf"
	"github.com/k0sproject/k0s/internal/testutil"
	"github.com/k0sproject/k0s/pkg/applier"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/discovery/cached/memory"
	discoveryfake "k8s.io/client-go/discovery/fake"
	clienttesting "k8s.io/client-go/testing"
	certutil "k8s.io/client-go/util/cert"
	"k8s.io/client-go/util/keyutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeriveKubeletServingCA(t *testing.T) {
	t.Run("matches golden values", func(t *testing.T) {
		// The kubelet-serving CA derived from a pinned cluster CA key. The
		// derivation is wire format: if these change, every kubelet in every
		// cluster gets a new kubelet-serving CA.
		const (
			scalar  = "61ea6b7b382506c0df930f4e0b428a2276c436c8c604f44e8c260fa23355745d"
			certSum = "3236fe8f0891d47113271e55da175d06ef07d3b92253ca19737f622b203ceec7"
		)

		clusterCAScalar, err := hex.DecodeString("0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20")
		require.NoError(t, err)
		clusterCAKey, err := ecdsa.ParseRawPrivateKey(elliptic.P256(), clusterCAScalar)
		require.NoError(t, err)
		notBefore := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
		clusterCACert := &x509.Certificate{NotBefore: notBefore, NotAfter: notBefore.AddDate(10, 0, 0)}
		material, err := kdf.FromPrivateKey(clusterCAKey)
		require.NoError(t, err)

		ca, err := DeriveKubeletServingCA(material, clusterCACert)
		require.NoError(t, err)
		derivedScalar, err := ca.Key.Bytes()
		require.NoError(t, err)
		assert.Equal(t, scalar, hex.EncodeToString(derivedScalar), "Derived key changed")
		derivedCertSum := sha256.Sum256(ca.Cert.Raw)
		assert.Equal(t, certSum, hex.EncodeToString(derivedCertSum[:]), "Derived certificate changed")
	})

	ca, clusterCACert := newTestKubeletServingCA(t)

	t.Run("is a self-signed server auth CA", func(t *testing.T) {
		assert.Equal(t, "CN=kubernetes-kubelet-serving-ca", ca.Cert.Subject.String())
		assert.Equal(t, ca.Cert.RawSubject, ca.Cert.RawIssuer, "Certificate isn't self-issued")
		assert.True(t, ca.Cert.IsCA, "Certificate isn't a CA")
		assert.Equal(t, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, ca.Cert.ExtKeyUsage, "CA should be restricted to server auth")
	})

	t.Run("follows the cluster CA validity", func(t *testing.T) {
		assert.True(t, ca.Cert.NotBefore.Equal(clusterCACert.NotBefore), "NotBefore differs from cluster CA")
		assert.True(t, ca.Cert.NotAfter.Equal(clusterCACert.NotAfter), "NotAfter differs from cluster CA")
	})

	t.Run("round-trips through PEM", func(t *testing.T) {
		keyPEM, err := ca.KeyPEM()
		require.NoError(t, err)
		key, err := keyutil.ParsePrivateKeyPEM(keyPEM)
		require.NoError(t, err)
		assert.True(t, ca.Key.Equal(key), "Key doesn't survive the PEM round trip")

		certs, err := certutil.ParseCertsPEM(ca.CertPEM())
		require.NoError(t, err)
		require.Len(t, certs, 1)
		assert.Equal(t, ca.Cert.Raw, certs[0].Raw, "Certificate doesn't survive the PEM round trip")
	})
}

// Generates a cluster CA and derives the kubelet-serving CA from it.
func newTestKubeletServingCA(t *testing.T) (*KubeletServingCA, *x509.Certificate) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	clusterCACert, err := certutil.NewSelfSignedCACert(certutil.Config{CommonName: "kubernetes-ca"}, key)
	require.NoError(t, err)
	material, err := kdf.FromPrivateKey(key)
	require.NoError(t, err)
	ca, err := DeriveKubeletServingCA(material, clusterCACert)
	require.NoError(t, err)
	return ca, clusterCACert
}

func TestKubeletServingTrustBundle(t *testing.T) {
	ca, clusterCACert := newTestKubeletServingCA(t)

	bundle := kubeletServingTrustBundle(ca, clusterCACert)

	certs, err := certutil.ParseCertsPEM(bundle)
	require.NoError(t, err)
	require.Len(t, certs, 2, "Bundle should hold exactly two certificates")
	assert.Equal(t, ca.Cert.Raw, certs[0].Raw, "Bundle should start with the kubelet-serving CA")
	assert.Equal(t, clusterCACert.Raw, certs[1].Raw, "Bundle should end with the cluster CA")
}

func TestKubeletServingCAPublisher(t *testing.T) {
	ca, clusterCACert := newTestKubeletServingCA(t)
	bundle := kubeletServingTrustBundle(ca, clusterCACert)

	// Starts a publisher and waits until it has published, using a fake API
	// server that serves ClusterTrustBundles unless requested otherwise.
	publish := func(t *testing.T, clusterTrustBundles bool) *testutil.FakeClientFactory {
		t.Helper()
		clients := testutil.NewFakeClientFactory()
		if !clusterTrustBundles {
			for _, list := range clients.DynamicClient.Resources {
				if list.GroupVersion == "certificates.k8s.io/v1" {
					list.APIResources = slices.DeleteFunc(list.APIResources, func(resource metav1.APIResource) bool {
						return resource.Name == "clustertrustbundles"
					})
				}
			}
		}

		underTest := KubeletServingCAPublisher{KubeletServingCA: ca, ClusterCACert: clusterCACert, Clients: clients}
		require.NoError(t, underTest.Init(t.Context()))
		require.NoError(t, underTest.Start(t.Context()))
		t.Cleanup(func() { assert.NoError(t, underTest.Stop()) })

		// The ConfigMap is the last resource to be applied.
		ctx := t.Context()
		require.EventuallyWithT(t, func(t *assert.CollectT) {
			_, err := clients.Client.CoreV1().ConfigMaps("kube-system").Get(ctx, "kubelet-serving-ca.crt", metav1.GetOptions{})
			assert.NoError(t, err)
		}, 10*time.Second, 10*time.Millisecond, "ConfigMap should have been published")
		return clients
	}

	assertConfigMap := func(t *testing.T, clients *testutil.FakeClientFactory) {
		t.Helper()
		configMap, err := clients.Client.CoreV1().ConfigMaps("kube-system").Get(t.Context(), "kubelet-serving-ca.crt", metav1.GetOptions{})
		require.NoError(t, err, "ConfigMap should have been published")
		assert.Equal(t, map[string]string{"ca.crt": string(bundle)}, configMap.Data, "ConfigMap should contain the bundle verbatim")
		assert.Equal(t, kubeletServingCAStackName, configMap.Labels[applier.NameLabel], "ConfigMap should belong to the stack")
	}

	t.Run("publishes a ConfigMap", func(t *testing.T) {
		clients := publish(t, false)
		assertConfigMap(t, clients)

		_, err := clients.Client.CertificatesV1().ClusterTrustBundles().Get(t.Context(), "kubernetes.io:kubelet-serving:k0s", metav1.GetOptions{})
		assert.True(t, apierrors.IsNotFound(err), "ClusterTrustBundle should not have been published: %v", err)
	})

	t.Run("publishes a ClusterTrustBundle if served", func(t *testing.T) {
		clients := publish(t, true)
		assertConfigMap(t, clients)

		ctb, err := clients.Client.CertificatesV1().ClusterTrustBundles().Get(t.Context(), "kubernetes.io:kubelet-serving:k0s", metav1.GetOptions{})
		require.NoError(t, err, "ClusterTrustBundle should have been published")
		assert.Equal(t, kubeletServingCAStackName, ctb.Labels[applier.NameLabel], "ClusterTrustBundle should belong to the stack")
		assert.Equal(t, "kubernetes.io/kubelet-serving", ctb.Spec.SignerName)
		assert.Equal(t, string(bundle), ctb.Spec.TrustBundle, "ClusterTrustBundle should contain the bundle verbatim")
	})
}

func TestServesClusterTrustBundles(t *testing.T) {
	newDiscovery := func(resources ...*metav1.APIResourceList) *discoveryfake.FakeDiscovery {
		return &discoveryfake.FakeDiscovery{Fake: &clienttesting.Fake{Resources: resources}}
	}

	t.Run("served", func(t *testing.T) {
		served, err := servesClusterTrustBundles(t.Context(), newDiscovery(&metav1.APIResourceList{
			GroupVersion: "certificates.k8s.io/v1",
			APIResources: []metav1.APIResource{{Name: "certificatesigningrequests"}, {Name: "clustertrustbundles"}},
		}))
		require.NoError(t, err)
		assert.True(t, served, "ClusterTrustBundles should be reported as served")
	})

	t.Run("not served", func(t *testing.T) {
		served, err := servesClusterTrustBundles(t.Context(), newDiscovery(&metav1.APIResourceList{
			GroupVersion: "certificates.k8s.io/v1",
			APIResources: []metav1.APIResource{{Name: "certificatesigningrequests"}},
		}))
		require.NoError(t, err)
		assert.False(t, served, "ClusterTrustBundles should not be reported as served")
	})

	t.Run("only served in a beta version", func(t *testing.T) {
		served, err := servesClusterTrustBundles(t.Context(), newDiscovery(&metav1.APIResourceList{
			GroupVersion: "certificates.k8s.io/v1beta1",
			APIResources: []metav1.APIResource{{Name: "clustertrustbundles"}},
		}))
		require.NoError(t, err)
		assert.False(t, served, "Only the stable API version counts")
	})

	t.Run("group version unknown to a cached client", func(t *testing.T) {
		served, err := servesClusterTrustBundles(t.Context(), memory.NewMemCacheClient(newDiscovery(&metav1.APIResourceList{
			GroupVersion: "certificates.k8s.io/v1beta1",
			APIResources: []metav1.APIResource{{Name: "clustertrustbundles"}},
		})))
		require.NoError(t, err)
		assert.False(t, served, "An unknown group version should count as not served")
	})

	t.Run("discovery fails", func(t *testing.T) {
		discovery := newDiscovery()
		discovery.PrependReactor("get", "resource", func(clienttesting.Action) (bool, runtime.Object, error) {
			return true, nil, errors.New("something went wrong")
		})
		_, err := servesClusterTrustBundles(t.Context(), discovery)
		assert.ErrorContains(t, err, "something went wrong")
	})
}
