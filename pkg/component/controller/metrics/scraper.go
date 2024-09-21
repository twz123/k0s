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

package metrics

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/k0sproject/k0s/pkg/certificate"
	"github.com/k0sproject/k0s/pkg/constant"
)

// A source for raw metrics.
type Scraper interface {
	Scrape(context.Context) (io.ReadCloser, error)
}

type ScraperFunc func(context.Context) (io.ReadCloser, error)

func (f ScraperFunc) Scrape(ctx context.Context) (io.ReadCloser, error) { return f(ctx) }

type HTTPScraperBuilder struct {
	URL  *url.URL
	opts []func(*http.Client) error
}

func (s *HTTPScraperBuilder) WithTimeout(timeout time.Duration) *HTTPScraperBuilder {
	s.opts = append(s.opts, func(c *http.Client) error {
		c.Timeout = timeout
		return nil
	})
	return s
}

func (s *HTTPScraperBuilder) WithCA(cert *certificate.Certificate) *HTTPScraperBuilder {
	s.opts = append(s.opts, func(c *http.Client) error {
		pool := x509.NewCertPool()
		if err := cert.AppendToPool(pool); err != nil {
			return fmt.Errorf("failed to set CA: %w", err)
		}
		c.Transport.(*http.Transport).TLSClientConfig.RootCAs = pool
		return nil
	})
	return s
}

func (s *HTTPScraperBuilder) WithCertificate(cert *certificate.Certificate) *HTTPScraperBuilder {
	s.opts = append(s.opts, func(c *http.Client) error {
		tlsCert, err := cert.Load()
		if err != nil {
			return fmt.Errorf("failed to set certificate: %w", err)
		}
		c.Transport.(*http.Transport).TLSClientConfig.Certificates = []tls.Certificate{tlsCert}
		return nil
	})
	return s
}

func (s *HTTPScraperBuilder) Build() (Scraper, error) {
	url, client := s.URL.String(), http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				MinVersion:   tls.VersionTLS12,
				CipherSuites: constant.AllowedTLS12CipherSuiteIDs,
			},
			DisableCompression: true,            // This is to be used on loopback connections.
			MaxIdleConns:       1,               // There won't be any concurrent connections.
			IdleConnTimeout:    1 * time.Minute, // The metrics scraper interval is 30 secs by default.
		},
		CheckRedirect: disallowRedirects,
	}

	for _, opt := range s.opts {
		if err := opt(&client); err != nil {
			return nil, err
		}
	}

	return ScraperFunc(func(ctx context.Context) (io.ReadCloser, error) {
		return scrapeHTTPMetrics(ctx, &client, url)
	}), nil
}

func scrapeHTTPMetrics(ctx context.Context, client *http.Client, endpoint string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request for %s: %w", endpoint, err)
	}

	if resp, err := client.Do(req); err != nil {
		return nil, err
	} else if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return resp.Body, nil
	} else {
		resp.Body.Close()
		return nil, &url.Error{
			Op:  "Get",
			URL: endpoint,
			Err: fmt.Errorf("non-successful status code: %s", resp.Status),
		}
	}
}

func disallowRedirects(req *http.Request, via []*http.Request) error {
	return fmt.Errorf("no redirects allowed: %s", req.URL)
}
