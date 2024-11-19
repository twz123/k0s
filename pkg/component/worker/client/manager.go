/*
Copyright 2024 k0s authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package client

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/k0sproject/k0s/pkg/component/manager"
	"github.com/k0sproject/k0s/pkg/kubernetes"
	utilnet "k8s.io/apimachinery/pkg/util/net"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	k8skubeletcert "k8s.io/kubernetes/pkg/kubelet/certificate"
)

type Manager struct {
	GetKubeconfig clientcmd.KubeconfigGetter

	restConfig *rest.Config
	cancel     context.CancelFunc

	certificate atomic.Pointer[tls.Certificate]
}

var _ manager.Component = (*Manager)(nil)

func (c *Manager) Init(context.Context) error {
	original, err := kubernetes.ClientConfig(c.GetKubeconfig)
	if err != nil {
		return err
	}

	if original.CertFile == "" {
		c.restConfig = original
		return nil
	}

	dynamic := rest.AnonymousClientConfig(original)
	tlsConfig, err := rest.TLSConfigFor(original)
	if err != nil {
		return err
	}

	c.certificate.Store(&tls.Certificate{Certificate: nil})
	tlsConfig.Certificates = nil
	tlsConfig.GetClientCertificate = func(cri *tls.CertificateRequestInfo) (*tls.Certificate, error) {
		return c.certificate.Load(), nil
	}

	dynamic.Transport = utilnet.SetTransportDefaults(&http.Transport{
		Proxy:               original.Proxy,
		TLSClientConfig:     tlsConfig,
		MaxIdleConnsPerHost: 25,
		DialContext:         original.Dial,
		DisableCompression:  original.DisableCompression,
	})

	c.restConfig = dynamic
	return nil
}

func (c *Manager) Start(context.Context) error {
	ctx, cancel := context.WithCancel(context.Background())

	go wait.UntilWithContext(ctx, c.runWatcher(), 1*time.Minute)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("failed to create watcher: %w", err)
	}

	go func() {
		defer func() {
			if err := watcher.Close(); err != nil {
				m.log.WithError(err).Error("Failed to close watcher")
			}
		}()
	}()

	if err != nil {
		m.log.WithError(err).Error("Failed to create watcher")
		return
	}
	defer func() {
		if err := watcher.Close(); err != nil {
			m.log.WithError(err).Error("Failed to close watcher")
		}
	}()

	err = watcher.Add(m.bundleDir)
	if err != nil {
		m.log.WithError(err).Error("Failed to watch bundle directory")
		return
	}

	if _, err := k8skubeletcert.UpdateTransport(ctx.Done(), c.restConfig, mgr, 0); err != nil {
		cancel()
		return err
	}

	c.cancel = cancel
	return nil
}

func (c *Manager) GetRESTConfig() *rest.Config {
	return rest.CopyConfig(c.restConfig)
}

func (c *Manager) Stop() error {
	panic("unimplemented")
}

func (c *CertificateManager) loadCertificate(certPath string) error {
	raw, err := os.ReadFile(certPath)
	cert, err := tls.LoadX509KeyPair(c.config.CertFile, c.config.KeyFile)
	if err != nil {
		return fmt.Errorf("can't load key pair: %w", err)
	}

	// the code borrowed from kubelet assumes Leaf is loaded which does not happen via tls.LoadX509KeyPair...
	cert.Leaf, err = x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return fmt.Errorf("can't parse certificate: %w", err)
	}

	c.certificate.Store(cert)
	return nil
}

func (c *Manager) runWatcher(ctx context.Context, certPath string) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		m.log.WithError(err).Error("Failed to create watcher")
		return
	}
	defer func() {
		if err := watcher.Close(); err != nil {
			m.log.WithError(err).Error("Failed to close watcher")
		}
	}()

	err = watcher.Add(m.bundleDir)
	if err != nil {
		m.log.WithError(err).Error("Failed to watch bundle directory")
		return
	}

	m.log.Info("Starting watch loop")

	for {
		select {
		case err := <-watcher.Errors:
			m.log.WithError(err).Error("Watch error")
			return

		case <-watcher.Events:
			if err := c.loadCertificate(); err != nil {
				m.log.WithError(err).Error("Failed to load certificate from ", certPath)
				return
			}

		case <-ctx.Done():
			m.log.Infof("Watch loop done")
			return
		}
	}
}
