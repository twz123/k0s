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
	_ "embed"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/k0sproject/k0s/internal/pkg/dir"
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
	K0sVars     *config.CfgVars
	restClient  rest.Interface
	storageType v1beta1.StorageType

	activeImage atomic.Pointer[string]
	tickerDone  context.CancelFunc
	jobs        []*job
}

var _ manager.Component = (*Component)(nil)
var _ manager.Reconciler = (*Component)(nil)

// NewComponent creates new Metrics reconciler
func NewComponent(k0sVars *config.CfgVars, clientCF kubernetes.ClientFactoryInterface, storageType v1beta1.StorageType) (*Component, error) {
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
		K0sVars:     k0sVars,
		restClient:  restClient,
	}, nil
}

// Init does nothing
func (c *Component) Init(_ context.Context) error {
	if err := dir.Init(filepath.Join(c.K0sVars.ManifestsDir, "metrics"), constant.ManifestsDirMode); err != nil {
		return err
	}

	var j *job
	j, err := c.newJob("kube-scheduler", "https://localhost:10259/metrics")
	if err != nil {
		return err
	}
	c.jobs = append(c.jobs, j)

	j, err = c.newJob("kube-controller-manager", "https://localhost:10257/metrics")
	if err != nil {
		return err
	}
	c.jobs = append(c.jobs, j)

	if c.storageType == v1beta1.EtcdStorageType {
		etcdJob, err := c.newEtcdJob()
		if err != nil {
			return err
		}
		c.jobs = append(c.jobs, etcdJob)
	}

	if c.storageType == v1beta1.KineStorageType {
		kineJob, err := c.newKineJob()
		if err != nil {
			return err
		}
		c.jobs = append(c.jobs, kineJob)
	}

	return nil
}

// Run runs the metric server reconciler
func (c *Component) Start(ctx context.Context) error {
	ctx, c.tickerDone = context.WithCancel(ctx)

	for _, j := range c.jobs {
		go c.run(ctx, j)
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
	scrapeURL    string
	name         string
	hostname     string
	scrapeClient *http.Client
}

func (c *Component) newEtcdJob() (*job, error) {
	certFile := path.Join(c.K0sVars.CertRootDir, "apiserver-etcd-client.crt")
	keyFile := path.Join(c.K0sVars.CertRootDir, "apiserver-etcd-client.key")

	httpClient, err := getClient(certFile, keyFile)
	if err != nil {
		return nil, err
	}

	return &job{
		scrapeURL:    "https://localhost:2379/metrics",
		name:         "etcd",
		hostname:     c.hostname,
		scrapeClient: httpClient,
	}, nil
}

func (c *Component) newKineJob() (*job, error) {
	httpClient, err := getClient("", "")
	if err != nil {
		return nil, err
	}

	return &job{
		scrapeURL:    "http://localhost:2380/metrics",
		name:         "kine",
		hostname:     c.hostname,
		scrapeClient: httpClient,
	}, nil
}

func (c *Component) newJob(name, scrapeURL string) (*job, error) {
	certFile := path.Join(c.K0sVars.CertRootDir, "admin.crt")
	keyFile := path.Join(c.K0sVars.CertRootDir, "admin.key")

	httpClient, err := getClient(certFile, keyFile)
	if err != nil {
		return nil, err
	}

	return &job{
		scrapeURL:    scrapeURL,
		name:         name,
		hostname:     c.hostname,
		scrapeClient: httpClient,
	}, nil
}

func (c *Component) run(ctx context.Context, j *job) {
	log := c.log.WithField("metrics_job", j.name)
	log.Debug("Running job")
	defer log.Debug("Stopped job")

	wait.NonSlidingUntilWithContext(ctx, func(ctx context.Context) {
		// Only start scraping if the pushgateway has been deployed
		if c.activeImage.Load() == nil {
			return
		}
		if err := c.collectAndPush(ctx, j); err != nil {
			log.WithError(err).Error("Failed to collect metrics")
		}
	}, time.Second*30)
}
func (j *job) pushURL() string {
	return fmt.Sprintf("/api/v1/namespaces/k0s-system/services/http:k0s-pushgateway:http/proxy/metrics/job/%s/instance/%s", j.name, j.hostname)
}

func (c *Component) collectAndPush(ctx context.Context, j *job) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, j.scrapeURL, nil)
	if err != nil {
		return fmt.Errorf("error creating GET request for %s: %w", j.scrapeURL, err)
	}

	resp, err := j.scrapeClient.Do(req)
	if err != nil {
		return fmt.Errorf("error collecting metrics from %s: %w", j.scrapeURL, err)
	}
	defer resp.Body.Close()

	res := c.restClient.Post().AbsPath(j.pushURL()).Body(resp.Body).Do(ctx)
	if res.Error() != nil {
		return fmt.Errorf("error sending POST request for job %s: %w", j.name, res.Error())
	}
	return nil
}

func getClient(certFile, keyFile string) (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = time.Minute
	tlsConfig := &tls.Config{InsecureSkipVerify: true}
	transport.TLSClientConfig = tlsConfig

	if certFile != "" && keyFile != "" {
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return nil, err
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	}
	return &http.Client{
		Transport: transport,
		Timeout:   time.Minute,
	}, nil
}
