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

package controller

import (
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"

	"github.com/k0sproject/k0s/internal/pkg/file"
	"github.com/k0sproject/k0s/internal/pkg/iface"
	"github.com/k0sproject/k0s/internal/pkg/stringslice"
	"github.com/k0sproject/k0s/pkg/apis/k0s.k0sproject.io/v1beta1"
	"github.com/k0sproject/k0s/pkg/certificate"
	"github.com/k0sproject/k0s/pkg/constant"

	"k8s.io/apimachinery/pkg/util/validation/field"
	utilnet "k8s.io/utils/net"

	"github.com/asaskevich/govalidator"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
)

var kubeconfigTemplate = template.Must(template.New("kubeconfig").Parse(`
apiVersion: v1
clusters:
- cluster:
    server: {{.URL}}
    certificate-authority-data: {{.CACert}}
  name: local
contexts:
- context:
    cluster: local
    namespace: default
    user: user
  name: Default
current-context: Default
kind: Config
preferences: {}
users:
- name: user
  user:
    client-certificate-data: {{.ClientCert}}
    client-key-data: {{.ClientKey}}
`))

// Certificates is the Component implementation to manage all k0s certs
type Certificates struct {
	K0sVars     constant.CfgVars
	NodeSpec    v1beta1.ClusterSpec
	CertManager certificate.Manager

	caCert string
}

// Init initializes the certificate component
func (c *Certificates) Init(ctx context.Context) error {
	eg, _ := errgroup.WithContext(ctx)
	// Common CA
	caCertPath := filepath.Join(c.K0sVars.CertRootDir, "ca.crt")
	caCertKey := filepath.Join(c.K0sVars.CertRootDir, "ca.key")

	if err := c.CertManager.EnsureCA("ca", "kubernetes-ca"); err != nil {
		return err
	}

	// We need CA cert loaded to generate client configs
	logrus.Debugf("CA key and cert exists, loading")
	cert, err := os.ReadFile(caCertPath)
	if err != nil {
		return fmt.Errorf("failed to read ca cert: %w", err)
	}
	c.caCert = string(cert)
	kubeConfigAPIUrl := (&url.URL{
		Scheme: "https",
		Host:   net.JoinHostPort("localhost", strconv.Itoa(c.NodeSpec.API.Port)),
	}).String()
	eg.Go(func() error {
		// Front proxy CA
		if err := c.CertManager.EnsureCA("front-proxy-ca", "kubernetes-front-proxy-ca"); err != nil {
			return err
		}

		proxyCertPath, proxyCertKey := filepath.Join(c.K0sVars.CertRootDir, "front-proxy-ca.crt"), filepath.Join(c.K0sVars.CertRootDir, "front-proxy-ca.key")

		proxyClientReq := certificate.Request{
			Name:   "front-proxy-client",
			CN:     "front-proxy-client",
			O:      "front-proxy-client",
			CACert: proxyCertPath,
			CAKey:  proxyCertKey,
		}
		_, err := c.CertManager.EnsureCertificate(proxyClientReq, constant.ApiserverUser)

		return err
	})

	eg.Go(func() error {
		// admin cert & kubeconfig
		adminReq := certificate.Request{
			Name:   "admin",
			CN:     "kubernetes-admin",
			O:      "system:masters",
			CACert: caCertPath,
			CAKey:  caCertKey,
		}
		adminCert, err := c.CertManager.EnsureCertificate(adminReq, "root")
		if err != nil {
			return err
		}

		if err := kubeConfig(c.K0sVars.AdminKubeConfigPath, kubeConfigAPIUrl, c.caCert, adminCert.Cert, adminCert.Key, "root"); err != nil {
			return err
		}

		return c.CertManager.CreateKeyPair("sa", constant.ApiserverUser)
	})

	eg.Go(func() error {
		// konnectivity kubeconfig
		konnectivityReq := certificate.Request{
			Name:   "konnectivity",
			CN:     "kubernetes-konnectivity",
			O:      "system:masters", // TODO: We need to figure out if konnectivity really needs superpowers
			CACert: caCertPath,
			CAKey:  caCertKey,
		}
		konnectivityCert, err := c.CertManager.EnsureCertificate(konnectivityReq, constant.KonnectivityServerUser)
		if err != nil {
			return err
		}

		return kubeConfig(
			c.K0sVars.KonnectivityKubeConfigPath,
			kubeConfigAPIUrl,
			c.caCert,
			konnectivityCert.Cert,
			konnectivityCert.Key,
			constant.KonnectivityServerUser,
		)
	})

	eg.Go(func() error {
		ccmReq := certificate.Request{
			Name:   "ccm",
			CN:     "system:kube-controller-manager",
			O:      "system:kube-controller-manager",
			CACert: caCertPath,
			CAKey:  caCertKey,
		}
		ccmCert, err := c.CertManager.EnsureCertificate(ccmReq, constant.ApiserverUser)
		if err != nil {
			return err
		}

		return kubeConfig(
			filepath.Join(c.K0sVars.CertRootDir, "ccm.conf"),
			kubeConfigAPIUrl,
			c.caCert,
			ccmCert.Cert,
			ccmCert.Key,
			constant.ApiserverUser,
		)
	})

	eg.Go(func() error {
		schedulerReq := certificate.Request{
			Name:   "scheduler",
			CN:     "system:kube-scheduler",
			O:      "system:kube-scheduler",
			CACert: caCertPath,
			CAKey:  caCertKey,
		}
		schedulerCert, err := c.CertManager.EnsureCertificate(schedulerReq, constant.SchedulerUser)
		if err != nil {
			return err
		}

		return kubeConfig(
			filepath.Join(c.K0sVars.CertRootDir, "scheduler.conf"),
			kubeConfigAPIUrl,
			c.caCert,
			schedulerCert.Cert,
			schedulerCert.Key,
			constant.SchedulerUser,
		)
	})

	eg.Go(func() error {
		kubeletClientReq := certificate.Request{
			Name:   "apiserver-kubelet-client",
			CN:     "apiserver-kubelet-client",
			O:      "system:masters",
			CACert: caCertPath,
			CAKey:  caCertKey,
		}
		_, err := c.CertManager.EnsureCertificate(kubeletClientReq, constant.ApiserverUser)
		return err
	})

	hostnames, err := c.gatherHostnames()
	if err != nil {
		return fmt.Errorf("failed to gather additional hostnames for TLS certificates: %w", err)
	}

	eg.Go(func() error {
		serverReq := certificate.Request{
			Name:      "server",
			CN:        "kubernetes",
			O:         "kubernetes",
			CACert:    caCertPath,
			CAKey:     caCertKey,
			Hostnames: hostnames,
		}
		_, err = c.CertManager.EnsureCertificate(serverReq, constant.ApiserverUser)

		return err
	})

	eg.Go(func() error {
		apiReq := certificate.Request{
			Name:      "k0s-api",
			CN:        "k0s-api",
			O:         "kubernetes",
			CACert:    caCertPath,
			CAKey:     caCertKey,
			Hostnames: hostnames,
		}
		// TODO Not sure about the user...
		_, err := c.CertManager.EnsureCertificate(apiReq, constant.ApiserverUser)
		return err
	})

	return eg.Wait()
}

func (c *Certificates) gatherHostnames() ([]string, error) {
	var hostnames []string

	if c.NodeSpec.API.ExternalAddress != "" {
		hostnames = append(hostnames, c.NodeSpec.API.ExternalAddress)
	}

	{ // cluster domain
		if !govalidator.IsDNSName(c.NodeSpec.Network.ClusterDomain) {
			return nil, fmt.Errorf("node config invalid: %w", field.Invalid(
				field.NewPath("spec", "network", "clusterDomain"),
				c.NodeSpec.Network.ClusterDomain,
				"not a valid DNS name",
			))
		}

		dnsLabels := append(
			[]string{"kubernetes", "default", "svc"},
			strings.Split(c.NodeSpec.Network.ClusterDomain, ".")...,
		)
		for i := range dnsLabels {
			hostnames = append(hostnames, strings.Join(dnsLabels[:i+1], "."))
		}
	}

	{ // local IPs
		hostnames = append(hostnames, "localhost", "127.0.0.1")
		localIPs, err := detectLocalIPs()
		if err != nil {
			return nil, fmt.Errorf("error detecting local IP: %w", err)
		}
		hostnames = append(hostnames, localIPs...)
		allAddresses, err := iface.AllAddresses()
		if err != nil {
			return nil, fmt.Errorf("failed to enumerate all network addresses: %w", err)
		}
		hostnames = append(hostnames, allAddresses...)
	}

	{ // Service CIDRs
		appendCIDR := func(cidrString string) error {
			_, cidr, err := net.ParseCIDR(cidrString)
			if err != nil {
				return fmt.Errorf("failed to parse CIDR %q: %w", cidr, err)
			}
			ip, err := utilnet.GetIndexedIP(cidr, 1)
			if err != nil {
				return fmt.Errorf("failed to get first IP address for CIDR %s: %w", cidrString, err)
			}
			hostnames = append(hostnames, ip.String())
			return nil
		}

		if err := appendCIDR(c.NodeSpec.Network.ServiceCIDR); err != nil {
			return nil, err
		}
		if c.NodeSpec.Network.DualStack.IsEnabled() {
			if err := appendCIDR(c.NodeSpec.Network.DualStack.IPv6ServiceCIDR); err != nil {
				return nil, err
			}
		}
	}

	return stringslice.Unique(hostnames), nil
}

func detectLocalIPs() ([]string, error) {
	var localIPs []string
	addrs, err := net.LookupIP("localhost")
	if err != nil {
		return nil, err
	}

	if hostname, err := os.Hostname(); err == nil {
		hostnameAddrs, err := net.LookupIP(hostname)
		if err == nil {
			addrs = append(addrs, hostnameAddrs...)
		}
	}

	for _, addr := range addrs {
		if addr.To4() != nil {
			localIPs = append(localIPs, addr.String())
		}
	}

	return localIPs, nil
}

func kubeConfig(dest, url, caCert, clientCert, clientKey, owner string) error {
	// We always overwrite the kubeconfigs as the certs might be regenerated at startup
	data := struct {
		URL        string
		CACert     string
		ClientCert string
		ClientKey  string
	}{
		URL:        url,
		CACert:     base64.StdEncoding.EncodeToString([]byte(caCert)),
		ClientCert: base64.StdEncoding.EncodeToString([]byte(clientCert)),
		ClientKey:  base64.StdEncoding.EncodeToString([]byte(clientKey)),
	}

	output, err := os.OpenFile(dest, os.O_RDWR|os.O_CREATE|os.O_TRUNC, constant.CertSecureMode)
	if err != nil {
		return err
	}
	defer output.Close()

	if err = kubeconfigTemplate.Execute(output, &data); err != nil {
		return err
	}

	return file.Chown(output.Name(), owner, constant.CertSecureMode)
}
