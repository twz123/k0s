// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"time"

	"github.com/k0sproject/k0s/pkg/applier"
	"github.com/k0sproject/k0s/pkg/component/manager"
	"github.com/k0sproject/k0s/pkg/kubernetes"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	certutil "k8s.io/client-go/util/cert"

	"github.com/sirupsen/logrus"
)

// KubeletServingCAPublisher publishes the trust bundle for kubelet serving
// certificates in the cluster, so that workloads can verify the certificates
// that kubelets serve. The bundle is read from the file that the API server
// verifies kubelets against, so that it holds exactly what the API server
// trusts. It's published as the kubelet-serving-ca.crt ConfigMap in the
// kube-system namespace, in the same shape as the kube-root-ca.crt ConfigMap
// that holds the cluster CA.
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
		err := p.tryPublish(ctx)
		if err == nil {
			p.log.Info("Published the kubelet-serving CA trust bundle")
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

func (p *KubeletServingCAPublisher) tryPublish(ctx context.Context) error {
	resources, err := p.resources()
	if err != nil {
		return err
	}

	return applier.ApplyStack(ctx, p.Clients, resources, kubeletServingCAStackName)
}

// Builds the resources to be published.
func (p *KubeletServingCAPublisher) resources() ([]*unstructured.Unstructured, error) {
	objects := []runtime.Object{
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "kubelet-serving-ca.crt",
				Namespace: metav1.NamespaceSystem,
			},
			Data: map[string]string{"ca.crt": string(p.bundle)},
		},
	}

	return applier.ToUnstructuredSlice(nil, objects...)
}
