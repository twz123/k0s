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
	_ "embed"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/k0sproject/k0s/internal/pkg/dir"
	internalnet "github.com/k0sproject/k0s/internal/pkg/net"
	"github.com/k0sproject/k0s/internal/pkg/templatewriter"
	"github.com/k0sproject/k0s/pkg/apis/k0s/v1beta1"
	"github.com/k0sproject/k0s/pkg/certificate"
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

	j, err := c.newKubernetesJob(&url.URL{
		Scheme: "https",
		Host:   net.JoinHostPort(c.loopbackIP.String(), "10259"),
		Path:   "/metrics",
	})
	if err != nil {
		return err
	}
	c.jobs["kube-scheduler"] = j

	j, err = c.newKubernetesJob(&url.URL{
		Scheme: "https",
		Host:   net.JoinHostPort(c.loopbackIP.String(), "10257"),
		Path:   "/metrics",
	})
	if err != nil {
		return err
	}
	c.jobs["kube-controller-manager"] = j

	if c.storageType == v1beta1.EtcdStorageType {
		job, err := c.newEtcdJob()
		if err != nil {
			return err
		}
		c.jobs["etcd"] = job
	}

	if c.storageType == v1beta1.KineStorageType {
		job, err := c.newKineJob()
		if err != nil {
			return err
		}
		c.jobs["kine"] = job
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

func (c *Component) newEtcdJob() (Scraper, error) {
	builder := HTTPScraperBuilder{URL: &url.URL{
		Scheme: "https",
		Host:   net.JoinHostPort(c.loopbackIP.String(), "2379"),
		Path:   "/metrics",
	}}

	return builder.
		WithTimeout(1 * time.Minute).
		WithCA(c.namedCert("etcd/ca")).
		WithCertificate(c.namedCert("apiserver-etcd-client")).
		Build()
}

func (c *Component) newKineJob() (Scraper, error) {
	builder := HTTPScraperBuilder{URL: &url.URL{
		Scheme: "http",
		Host:   net.JoinHostPort(c.loopbackIP.String(), "2380"),
		Path:   "/metrics",
	}}

	return builder.WithTimeout(1 * time.Minute).Build()
}

func (c *Component) newKubernetesJob(scrapeURL *url.URL) (Scraper, error) {
	builder := HTTPScraperBuilder{URL: scrapeURL}
	return builder.
		WithTimeout(1 * time.Minute).
		WithCertificate(c.namedCert("admin")).
		Build()
}

func (c *Component) namedCert(name string) *certificate.Certificate {
	return &certificate.Certificate{
		CertPath: filepath.Join(c.K0sVars.CertRootDir, name+".crt"),
		KeyPath:  filepath.Join(c.K0sVars.CertRootDir, name+".key"),
	}
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
