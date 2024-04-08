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
	"github.com/k0sproject/k0s/internal/pkg/users"
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
	CACert      string
	CertManager certificate.Manager
	ClusterSpec *v1beta1.ClusterSpec
	K0sVars     *config.CfgVars
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
	c.CACert = string(cert)

	apiServerUID, err := users.LookupUID(constant.ApiserverUser)
	if err != nil {
		err = fmt.Errorf("failed to lookup UID for %q: %w", constant.ApiserverUser, err)
		apiServerUID = users.RootUID
		logrus.WithError(err).Warn("Files with key material for kube-apiserver user will be owned by root")
	}

	// front-proxy-client
	eg.Go(func() error {
		if err := c.CertManager.EnsureCA("front-proxy-ca", "kubernetes-front-proxy-ca"); err != nil {
			return err
		}

		req := certificate.Request{
			Name:   "front-proxy-client",
			CN:     "front-proxy-client",
			O:      "front-proxy-client",
			CACert: filepath.Join(c.K0sVars.CertRootDir, "front-proxy-ca.crt"),
			CAKey:  filepath.Join(c.K0sVars.CertRootDir, "front-proxy-ca.key"),
		}
		_, err := c.CertManager.EnsureCertificate(req, apiServerUID)

		return err
	})

	// admin
	eg.Go(func() error {
		req := certificate.Request{
			Name:   "admin",
			CN:     "kubernetes-admin",
			O:      "system:masters",
			CACert: caCertPath,
			CAKey:  caCertKey,
		}
		cert, err := c.CertManager.EnsureCertificate(req, users.RootUID)
		if err != nil {
			return err
		}

		if err := c.kubeConfig(c.K0sVars.AdminKubeConfigPath, &cert, users.RootUID); err != nil {
			return err
		}

		return c.CertManager.CreateKeyPair("sa", c.K0sVars, apiServerUID)
	})

	// konnectivity
	eg.Go(func() error {
		req := certificate.Request{
			Name:   "konnectivity",
			CN:     "kubernetes-konnectivity",
			O:      "system:masters", // TODO: We need to figure out if konnectivity really needs superpowers
			CACert: caCertPath,
			CAKey:  caCertKey,
		}

		uid, err := users.LookupUID(constant.KonnectivityServerUser)
		if err != nil {
			err = fmt.Errorf("failed to lookup UID for %q: %w", constant.KonnectivityServerUser, err)
			uid = users.RootUID
			logrus.WithError(err).Warn("Files with key material for konnectivity-server user will be owned by root")
		}

		cert, err := c.CertManager.EnsureCertificate(req, uid)
		if err != nil {
			return err
		}

		return c.kubeConfig(c.K0sVars.KonnectivityKubeConfigPath, &cert, uid)
	})

	// controller manager
	eg.Go(func() error {
		req := certificate.Request{
			Name:   "ccm",
			CN:     "system:kube-controller-manager",
			O:      "system:kube-controller-manager",
			CACert: caCertPath,
			CAKey:  caCertKey,
		}
		cert, err := c.CertManager.EnsureCertificate(req, apiServerUID)
		if err != nil {
			return err
		}

		return c.kubeConfig(filepath.Join(c.K0sVars.CertRootDir, "ccm.conf"), &cert, apiServerUID)
	})

	// scheduler
	eg.Go(func() error {
		req := certificate.Request{
			Name:   "scheduler",
			CN:     "system:kube-scheduler",
			O:      "system:kube-scheduler",
			CACert: caCertPath,
			CAKey:  caCertKey,
		}

		uid, err := users.LookupUID(constant.SchedulerUser)
		if err != nil {
			err = fmt.Errorf("failed to lookup UID for %q: %w", constant.SchedulerUser, err)
			uid = users.RootUID
			logrus.WithError(err).Warn("Files with key material for kube-scheduler user will be owned by root")
		}

		cert, err := c.CertManager.EnsureCertificate(req, uid)
		if err != nil {
			return err
		}

		return c.kubeConfig(filepath.Join(c.K0sVars.CertRootDir, "scheduler.conf"), &cert, uid)
	})

	// kubelet
	eg.Go(func() error {
		req := certificate.Request{
			Name:   "apiserver-kubelet-client",
			CN:     "apiserver-kubelet-client",
			O:      "system:masters",
			CACert: caCertPath,
			CAKey:  caCertKey,
		}
		_, err := c.CertManager.EnsureCertificate(req, apiServerUID)
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

	// server
	eg.Go(func() error {
		req := certificate.Request{
			Name:      "server",
			CN:        "kubernetes",
			O:         "kubernetes",
			CACert:    caCertPath,
			CAKey:     caCertKey,
			Hostnames: hostnames,
		}
		_, err = c.CertManager.EnsureCertificate(req, apiServerUID)
		return err
	})

	// API
	eg.Go(func() error {
		req := certificate.Request{
			Name:      "k0s-api",
			CN:        "k0s-api",
			O:         "kubernetes",
			CACert:    caCertPath,
			CAKey:     caCertKey,
			Hostnames: hostnames,
		}
		// TODO Not sure about the user...
		_, err := c.CertManager.EnsureCertificate(req, apiServerUID)
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

func (c *Certificates) kubeConfig(dest string, cert *certificate.Certificate, ownerID int) error {
	// We always overwrite the kubeconfigs as the certs might be regenerated at startup
	const (
		clusterName = "local"
		contextName = "Default"
		userName    = "user"
	)

	kubeconfig, err := clientcmd.Write(clientcmdapi.Config{
		Clusters: map[string]*clientcmdapi.Cluster{clusterName: {
			// Changing the server URL here also requires changes in the "k0s
			// kubeconfig admin" subcommand, as it will replace the internal URL
			// with the external one by checking for string equality.
			Server:                   fmt.Sprintf("https://localhost:%d", c.ClusterSpec.API.Port),
			CertificateAuthorityData: []byte(c.CACert),
		}},
		Contexts: map[string]*clientcmdapi.Context{contextName: {
			Cluster:  clusterName,
			AuthInfo: userName,
		}},
		CurrentContext: contextName,
		AuthInfos: map[string]*clientcmdapi.AuthInfo{userName: {
			ClientCertificateData: []byte(cert.Cert),
			ClientKeyData:         []byte(cert.Key),
		}},
	})
	if err != nil {
		return err
	}

	err = file.WriteContentAtomically(dest, kubeconfig, constant.CertSecureMode)
	if err != nil {
		return err
	}

	return file.Chown(dest, ownerID, constant.CertSecureMode)
}
