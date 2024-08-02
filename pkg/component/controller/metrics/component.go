/*
Copyright 2022 k0s authors

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

package metrics

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/k0sproject/k0s/internal/pkg/dir"
	internalnet "github.com/k0sproject/k0s/internal/pkg/net"
	"github.com/k0sproject/k0s/internal/pkg/templatewriter"
	"github.com/k0sproject/k0s/pkg/apis/k0s/v1beta1"
	"github.com/k0sproject/k0s/pkg/component/manager"
	"github.com/k0sproject/k0s/pkg/config"
	"github.com/k0sproject/k0s/pkg/constant"
	"github.com/k0sproject/k0s/pkg/kubernetes"

	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"

	"github.com/sirupsen/logrus"
)

//go:embed pushgateway.yaml
var pushgatewayTemplate string

// Component is the reconciler implementation for metrics server
type Component struct {
	log logrus.FieldLogger

	hostname    string
	loopbackIP  net.IP
	K0sVars     *config.CfgVars
	restClient  rest.Interface
	storageType v1beta1.StorageType

	activeImage atomic.Pointer[string]
	tickerDone  context.CancelFunc
	jobs        map[string]Scraper
}

var _ manager.Component = (*Component)(nil)
var _ manager.Reconciler = (*Component)(nil)

// NewComponent creates new Metrics reconciler
func NewComponent(k0sVars *config.CfgVars, loopbackIP net.IP, clientCF kubernetes.ClientFactoryInterface, storageType v1beta1.StorageType) (*Component, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return nil, err
	}

	restClient, err := clientCF.GetRESTClient()
	if err != nil {
		return nil, fmt.Errorf("error getting REST client for metrics: %w", err)
	}

	return &Component{
		log:         logrus.WithFields(logrus.Fields{"component": "metrics"}),
		storageType: storageType,
		hostname:    hostname,
		loopbackIP:  loopbackIP,
		K0sVars:     k0sVars,
		restClient:  restClient,
		jobs:        make(map[string]Scraper),
	}, nil
}

// Init does nothing
func (c *Component) Init(ctx context.Context) error {
	if err := dir.Init(filepath.Join(c.K0sVars.ManifestsDir, "metrics"), constant.ManifestsDirMode); err != nil {
		return err
	}

	loopbackIP, err := internalnet.LookupLoopbackIP(ctx)
	if err != nil {
		if errors.Is(err, ctx.Err()) {
			return err
		}
		c.log.WithError(err).Errorf("Falling back to %s as bind address", loopbackIP)
	}

	j, err := c.newKubernetesJob("https://" + net.JoinHostPort(c.loopbackIP.String(), "10259") + "/metrics")
	if err != nil {
		return err
	}
	c.jobs["kube-scheduler"] = j

	j, err = c.newKubernetesJob("https://" + net.JoinHostPort(c.loopbackIP.String(), "10257") + "/metrics")
	if err != nil {
		return err
	}
	c.jobs["kube-controller-manager"] = j

	if c.storageType == v1beta1.EtcdStorageType {
		etcdJob, err := c.newEtcdJob()
		if err != nil {
			return err
		}
		c.jobs["etcd"] = etcdJob
	}

	if c.storageType == v1beta1.KineStorageType {
		kineJob, err := c.newKineJob()
		if err != nil {
			return err
		}
		c.jobs["kine"] = kineJob
	}

	return nil
}

// Run runs the metric server reconciler
func (c *Component) Start(ctx context.Context) error {
	ctx, c.tickerDone = context.WithCancel(ctx)

	for jobName, scraper := range c.jobs {
		go c.run(ctx, jobName, scraper)
	}

	return nil
}

// Stop stops the reconciler
func (c *Component) Stop() error {
	if c.tickerDone != nil {
		c.tickerDone()
	}
	return nil
}

// Reconcile detects changes in configuration and applies them to the component
func (c *Component) Reconcile(_ context.Context, clusterConfig *v1beta1.ClusterConfig) error {
	c.log.Debug("reconcile method called for: Metrics")

	activeImage, newImage := c.activeImage.Load(), clusterConfig.Spec.Images.PushGateway.URI()
	if activeImage == nil || newImage != *activeImage {
		tw := templatewriter.TemplateWriter{
			Path:     filepath.Join(c.K0sVars.ManifestsDir, "metrics", "pushgateway.yaml"),
			Name:     "pushgateway-with-ttl",
			Template: pushgatewayTemplate,
			Data: map[string]string{
				"Image": newImage,
			},
		}
		if err := tw.Write(); err != nil {
			return err
		}
		c.activeImage.Store(&newImage)
		c.log.Debug("Wrote pushgateway manifest")
	}

	return nil
}

type job struct {
	scrapeURL     string
	scrapeClient  *http.Client
	scrapeTimeout time.Duration
}

func (c *Component) newEtcdJob() (*job, error) {
	rootCAs, err := c.loadRootCAs("etcd/ca")
	if err != nil {
		return nil, err
	}
	clientCerts, err := c.loadClientCerts("apiserver-etcd-client")
	if err != nil {
		return nil, err
	}

	return &job{
		scrapeURL: "https://localhost:2379/metrics",
		scrapeClient: newHttpClient(&tls.Config{
			RootCAs:      rootCAs,
			Certificates: clientCerts,
		}),
		scrapeTimeout: 1 * time.Minute,
	}, nil
}

func (c *Component) newKineJob() (*job, error) {
	return &job{
		scrapeURL:     "http://localhost:2380/metrics",
		scrapeClient:  newHttpClient(nil),
		scrapeTimeout: 1 * time.Minute,
	}, nil
}

func (c *Component) newKubernetesJob(scrapeURL string) (*job, error) {
	rootCAs, err := c.loadRootCAs("ca")
	if err != nil {
		return nil, err
	}
	clientCerts, err := c.loadClientCerts("admin")
	if err != nil {
		return nil, err
	}

	return &job{
		scrapeURL: scrapeURL,
		scrapeClient: newHttpClient(&tls.Config{
			RootCAs:      rootCAs,
			Certificates: clientCerts,
		}),
		scrapeTimeout: 1 * time.Minute,
	}, nil
}

func (c *Component) loadRootCAs(name string) (*x509.CertPool, error) {
	rootCAs := x509.NewCertPool()
	if rootCA, err := os.ReadFile(filepath.Join(c.K0sVars.CertRootDir, name+".crt")); err != nil {
		return nil, fmt.Errorf("failed to load root TLS certificates: %w", err)
	} else if ok := rootCAs.AppendCertsFromPEM(rootCA); !ok {
		return nil, fmt.Errorf("failed to append root TLS certificates to pool")
	}

	return rootCAs, nil
}

func (c *Component) loadClientCerts(name string) ([]tls.Certificate, error) {
	certFile := path.Join(c.K0sVars.CertRootDir, name+".crt")
	keyFile := path.Join(c.K0sVars.CertRootDir, name+".key")
	clientCert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load key pair: %w", err)
	}

	return []tls.Certificate{clientCert}, nil
}

func (c *Component) run(ctx context.Context, jobName string, s Scraper) {
	log := c.log.WithField("metrics_job", jobName)
	log.Debug("Running job")
	defer log.Debug("Stopped job")

	wait.NonSlidingUntilWithContext(ctx, func(ctx context.Context) {
		// Only start scraping if the pushgateway has been deployed
		if c.activeImage.Load() == nil {
			return
		}
		if err := c.collectAndPush(ctx, jobName, s); err != nil {
			log.WithError(err).Error("Failed to collect metrics")
		}
	}, time.Second*30)
}

func (c *Component) collectAndPush(ctx context.Context, jobName string, s Scraper) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	metrics, err := s.Scrape(ctx)
	if err != nil {
		return err
	}

	if err := c.restClient.Post().Prefix("api", "v1").
		Resource("services").Namespace("k0s-system").Name("http:k0s-pushgateway:http").
		SubResource("proxy").Suffix("metrics", "job", jobName, "instance", c.hostname).
		Body(metrics).
		Do(ctx).
		Error(); err != nil {
		return fmt.Errorf("failed to push metrics: %w", err)
	}

	return nil
}

func (j *job) Scrape(ctx context.Context) (_ io.ReadCloser, err error) {
	ctx, cancel := context.WithTimeout(ctx, j.scrapeTimeout)
	defer func() {
		if err != nil {
			cancel()
		}
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, j.scrapeURL, nil)
	if err != nil {
		return nil, fmt.Errorf("error creating %s request for %s: %w", http.MethodGet, j.scrapeURL, err)
	}

	if resp, err := j.scrapeClient.Do(req); err != nil {
		return nil, err
	} else if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return &cancelingReadCloser{resp.Body, cancel}, nil
	} else {
		resp.Body.Close()
		return nil, &url.Error{
			Op:  "Get",
			URL: j.scrapeURL,
			Err: fmt.Errorf("non-successful status code: %s", resp.Status),
		}
	}
}

func newHttpClient(tlsConfig *tls.Config) *http.Client {
	if tlsConfig == nil {
		tlsConfig = new(tls.Config)
	}
	tlsConfig.MinVersion = tls.VersionTLS12
	tlsConfig.CipherSuites = constant.AllowedTLS12CipherSuiteIDs

	return &http.Client{
		Transport: &http.Transport{
			TLSClientConfig:    tlsConfig,
			DisableCompression: true,            // This is to be used on loopback connections.
			MaxIdleConns:       1,               // There won't be any concurrent connections.
			IdleConnTimeout:    1 * time.Minute, // The metrics scraper interval is 30 secs by default.
		},
		CheckRedirect: disallowRedirects,
	}
}

func disallowRedirects(req *http.Request, via []*http.Request) error {
	return fmt.Errorf("no redirects allowed: %s", req.URL)
}

type cancelingReadCloser struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c *cancelingReadCloser) Close() error {
	defer c.cancel()
	return c.ReadCloser.Close()
}
