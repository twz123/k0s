/*
Copyright 2020 k0s authors

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
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"github.com/k0sproject/k0s/internal/pkg/file"
	"github.com/k0sproject/k0s/pkg/apis/k0s/v1beta1"
	"github.com/k0sproject/k0s/pkg/certificate"
	"github.com/k0sproject/k0s/pkg/config"
	"github.com/k0sproject/k0s/pkg/constant"

	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
)

// Certificates is the Component implementation to manage all k0s certs
type Certificates struct {
	CertManager certificate.Manager
	ClusterSpec *v1beta1.ClusterSpec
	K0sVars     *config.CfgVars
}

// Init initializes the certificate component
func (c *Certificates) Init(ctx context.Context) error {
	eg, _ := errgroup.WithContext(ctx)
	// Common CA
	caCert, err := c.CertManager.EnsureCA("ca", "kubernetes-ca")
	if err != nil {
		return err
	}

	// We need CA cert loaded to generate client configs
	logrus.Debugf("CA key and cert exists, loading")
	caData, err := os.ReadFile(caCert.CertPath)
	if err != nil {
		return fmt.Errorf("failed to read ca cert: %w", err)
	}
	// Changing the URL here also requires changes in the "k0s kubeconfig admin" subcommand.
	kubeConfigAPIUrl := fmt.Sprintf("https://localhost:%d", c.ClusterSpec.API.Port)
	eg.Go(func() error {
		// Front proxy CA
		if _, err := c.CertManager.EnsureCA("front-proxy-ca", "kubernetes-front-proxy-ca"); err != nil {
			return err
		}

		proxyClientReq := certificate.Request{
			Name: "front-proxy-client",
			CN:   "front-proxy-client",
			O:    "front-proxy-client",
			CA:   "front-proxy-ca",
		}
		_, err := c.CertManager.EnsureCertificate(proxyClientReq, constant.ApiserverUser)

		return err
	})

	eg.Go(func() error {
		// admin cert & kubeconfig
		adminReq := certificate.Request{
			Name: "admin",
			CN:   "kubernetes-admin",
			O:    "system:masters",
		}
		adminCert, err := c.CertManager.EnsureCertificate(adminReq, "root")
		if err != nil {
			return err
		}

		if err := kubeConfig(c.K0sVars.AdminKubeConfigPath, kubeConfigAPIUrl, caData, adminCert, "root"); err != nil {
			return err
		}

		return c.CertManager.CreateKeyPair("sa", constant.ApiserverUser)
	})

	eg.Go(func() error {
		// konnectivity kubeconfig
		konnectivityReq := certificate.Request{
			Name: "konnectivity",
			CN:   "kubernetes-konnectivity",
			O:    "system:masters", // TODO: We need to figure out if konnectivity really needs superpowers
		}
		konnectivityCert, err := c.CertManager.EnsureCertificate(konnectivityReq, constant.KonnectivityServerUser)
		if err != nil {
			return err
		}
		return kubeConfig(c.K0sVars.KonnectivityKubeConfigPath, kubeConfigAPIUrl, caData, konnectivityCert, constant.KonnectivityServerUser)
	})

	eg.Go(func() error {
		ccmReq := certificate.Request{
			Name: "ccm",
			CN:   "system:kube-controller-manager",
			O:    "system:kube-controller-manager",
		}
		ccmCert, err := c.CertManager.EnsureCertificate(ccmReq, constant.ApiserverUser)
		if err != nil {
			return err
		}

		return kubeConfig(filepath.Join(c.K0sVars.CertRootDir, "ccm.conf"), kubeConfigAPIUrl, caData, ccmCert, constant.ApiserverUser)
	})

	eg.Go(func() error {
		schedulerReq := certificate.Request{
			Name: "scheduler",
			CN:   "system:kube-scheduler",
			O:    "system:kube-scheduler",
		}
		schedulerCert, err := c.CertManager.EnsureCertificate(schedulerReq, constant.SchedulerUser)
		if err != nil {
			return err
		}

		return kubeConfig(filepath.Join(c.K0sVars.CertRootDir, "scheduler.conf"), kubeConfigAPIUrl, caData, schedulerCert, constant.SchedulerUser)
	})

	eg.Go(func() error {
		kubeletClientReq := certificate.Request{
			Name: "apiserver-kubelet-client",
			CN:   "apiserver-kubelet-client",
			O:    "system:masters",
		}
		_, err := c.CertManager.EnsureCertificate(kubeletClientReq, constant.ApiserverUser)
		return err
	})

	hostnames := []string{
		"kubernetes",
		"kubernetes.default",
		"kubernetes.default.svc",
		"kubernetes.default.svc.cluster",
		fmt.Sprintf("kubernetes.svc.%s", c.ClusterSpec.Network.ClusterDomain),
		"localhost",
		"127.0.0.1",
	}

	localIPs, err := detectLocalIPs(ctx)
	if err != nil {
		return fmt.Errorf("error detecting local IP: %w", err)
	}
	hostnames = append(hostnames, localIPs...)
	hostnames = append(hostnames, c.ClusterSpec.API.Sans()...)

	// Add to SANs the IPs from the control plane load balancer
	cplb := c.ClusterSpec.Network.ControlPlaneLoadBalancing
	if cplb != nil && cplb.Enabled && cplb.Keepalived != nil {
		for _, v := range cplb.Keepalived.VRRPInstances {
			for _, vip := range v.VirtualIPs {
				ip, _, err := net.ParseCIDR(vip)
				if err != nil {
					return fmt.Errorf("error parsing virtualIP %s: %w", vip, err)
				}
				hostnames = append(hostnames, ip.String())
			}
		}
	}

	internalAPIAddress, err := c.ClusterSpec.Network.InternalAPIAddresses()
	if err != nil {
		return err
	}
	hostnames = append(hostnames, internalAPIAddress...)

	eg.Go(func() error {
		serverReq := certificate.Request{
			Name:      "server",
			CN:        "kubernetes",
			O:         "kubernetes",
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
			Hostnames: hostnames,
		}
		// TODO Not sure about the user...
		_, err := c.CertManager.EnsureCertificate(apiReq, constant.ApiserverUser)
		return err
	})

	return eg.Wait()
}

func detectLocalIPs(ctx context.Context) ([]string, error) {
	resolver := net.DefaultResolver

	addrs, err := resolver.LookupIPAddr(ctx, "localhost")
	if err != nil {
		return nil, err
	}

	if hostname, err := os.Hostname(); err == nil {
		hostnameAddrs, err := resolver.LookupIPAddr(ctx, hostname)
		if err == nil {
			addrs = append(addrs, hostnameAddrs...)
		} else if errors.Is(err, ctx.Err()) {
			return nil, err
		}
	}

	var localIPs []string
	for _, addr := range addrs {
		ip := addr.IP
		if ip.To4() != nil || ip.To16() != nil {
			localIPs = append(localIPs, ip.String())
		}
	}

	return localIPs, nil
}

func kubeConfig(dest, url string, caData []byte, clientCert *certificate.Certificate, owner string) error {
	certData, err := os.ReadFile(clientCert.CertPath)
	if err != nil {
		return err
	}
	keyData, err := os.ReadFile(clientCert.KeyPath)
	if err != nil {
		return err
	}

	// We always overwrite the kubeconfigs as the certs might be regenerated at startup
	const (
		clusterName = "local"
		contextName = "Default"
		userName    = "user"
	)

	kubeconfig, err := clientcmd.Write(clientcmdapi.Config{
		Clusters: map[string]*clientcmdapi.Cluster{clusterName: {
			// The server URL is replaced in the "k0s kubeconfig admin" subcommand.
			Server:                   url,
			CertificateAuthorityData: caData,
		}},
		Contexts: map[string]*clientcmdapi.Context{contextName: {
			Cluster:  clusterName,
			AuthInfo: userName,
		}},
		CurrentContext: contextName,
		AuthInfos: map[string]*clientcmdapi.AuthInfo{userName: {
			ClientCertificateData: certData,
			ClientKeyData:         keyData,
		}},
	})
	if err != nil {
		return err
	}

	err = file.WriteContentAtomically(dest, kubeconfig, constant.CertSecureMode)
	if err != nil {
		return err
	}

	return file.Chown(dest, owner, constant.CertSecureMode)
}
