// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/k0sproject/k0s/internal/testutil"
	"github.com/k0sproject/k0s/pkg/applier"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/discovery/cached/memory"
	discoveryfake "k8s.io/client-go/discovery/fake"
	clienttesting "k8s.io/client-go/testing"
	certutil "k8s.io/client-go/util/cert"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Generates a self-signed CA certificate with the given common name.
func newTestCACert(t *testing.T, commonName string) *x509.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	cert, err := certutil.NewSelfSignedCACert(certutil.Config{CommonName: commonName}, key)
	require.NoError(t, err)
	return cert
}

// Writes the given content to a file in a temporary directory.
func writeTestBundleFile(t *testing.T, content []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "ca.crt")
	require.NoError(t, os.WriteFile(path, content, 0o600))
	return path
}

func TestReadKubeletServingTrustBundle(t *testing.T) {
	t.Run("re-encodes the certificates", func(t *testing.T) {
		first, second := newTestCACert(t, "first"), newTestCACert(t, "second")
		bundle := kubeletServingTrustBundle(first, second)

		// Format the file in ways that a re-encoding should get rid of: a
		// leading comment, CRLF line endings and trailing whitespace.
		content := slices.Concat([]byte("# The cluster CA\n"), bundle, []byte("\n\n"))
		content = bytes.ReplaceAll(content, []byte("\n"), []byte("\r\n"))

		read, err := readKubeletServingTrustBundle(writeTestBundleFile(t, content))
		require.NoError(t, err)
		assert.Equal(t, string(bundle), string(read), "Bundle should be canonically encoded")
	})

	t.Run("fails without certificates", func(t *testing.T) {
		_, err := readKubeletServingTrustBundle(writeTestBundleFile(t, []byte("not a certificate\n")))
		assert.ErrorContains(t, err, "failed to parse kubelet-serving CA trust bundle")
	})

	t.Run("fails if the file is missing", func(t *testing.T) {
		_, err := readKubeletServingTrustBundle(filepath.Join(t.TempDir(), "missing.crt"))
		assert.ErrorIs(t, err, os.ErrNotExist)
	})
}

func TestKubeletServingTrustBundle(t *testing.T) {
	first, second := newTestCACert(t, "first"), newTestCACert(t, "second")

	bundle := kubeletServingTrustBundle(first, second)

	certs, err := certutil.ParseCertsPEM(bundle)
	require.NoError(t, err)
	require.Len(t, certs, 2, "Bundle should hold exactly two certificates")
	assert.Equal(t, first.Raw, certs[0].Raw, "Bundle should keep the order")
	assert.Equal(t, second.Raw, certs[1].Raw, "Bundle should keep the order")
}

func TestKubeletServingCAPublisher(t *testing.T) {
	bundle := kubeletServingTrustBundle(newTestCACert(t, "kubernetes-ca"))
	bundleFile := writeTestBundleFile(t, bundle)

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

		underTest := KubeletServingCAPublisher{BundleFile: bundleFile, Clients: clients}
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

	t.Run("fails to initialize without a bundle", func(t *testing.T) {
		underTest := KubeletServingCAPublisher{BundleFile: filepath.Join(t.TempDir(), "missing.crt")}
		assert.ErrorIs(t, underTest.Init(t.Context()), os.ErrNotExist)
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
