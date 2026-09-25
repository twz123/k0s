// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"time"

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

	"github.com/sirupsen/logrus"
)

// KubeletServingCAPublisher publishes the trust bundle for kubelet serving
// certificates in the cluster, so that workloads can verify the certificates
// that kubelets serve. The bundle is read from the file that the API server
// verifies kubelets against, so that it holds exactly what the API server
// trusts. It's published as the kubelet-serving-ca.crt ConfigMap in the
// kube-system namespace, in the same shape as the kube-root-ca.crt ConfigMap
// that holds the cluster CA. If the API server serves ClusterTrustBundles, it
// is also published as a ClusterTrustBundle for the
// kubernetes.io/kubelet-serving signer, which can be mounted in any namespace.
type KubeletServingCAPublisher struct {
	// Path of the file that the API server verifies kubelet serving
	// certificates against, see [APIServer.KubeletCertificateAuthorityFile].
	BundleFile string
	Clients    kubernetes.ClientFactoryInterface

	log    logrus.FieldLogger
	bundle []byte
	stop   context.CancelFunc
	done   <-chan struct{}
}

var _ manager.Component = (*KubeletServingCAPublisher)(nil)

const kubeletServingCAStackName = "kubelet-serving-ca"

// Init implements [manager.Component].
func (p *KubeletServingCAPublisher) Init(context.Context) error {
	p.log = logrus.WithField("component", "kubelet-serving-ca")

	bundle, err := readKubeletServingTrustBundle(p.BundleFile)
	if err != nil {
		return err
	}
	p.bundle = bundle

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

// Reads the trust bundle for kubelet serving certificates from the given
// file. The certificates are re-encoded, so that the bundle doesn't depend on
// how the file happens to be formatted.
func readKubeletServingTrustBundle(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read kubelet-serving CA trust bundle: %w", err)
	}
	certs, err := certutil.ParseCertsPEM(data)
	if err != nil {
		return nil, fmt.Errorf("failed to parse kubelet-serving CA trust bundle %q: %w", path, err)
	}
	return kubeletServingTrustBundle(certs...), nil
}

// Assembles the PEM-encoded trust bundle for kubelet serving certificates from
// the given certificates, in order.
func kubeletServingTrustBundle(certs ...*x509.Certificate) []byte {
	var bundle []byte
	for _, cert := range certs {
		bundle = append(bundle, pem.EncodeToMemory(&pem.Block{
			Type: certutil.CertificateBlockType, Bytes: cert.Raw,
		})...)
	}
	return bundle
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
	bundle := string(p.bundle)

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
