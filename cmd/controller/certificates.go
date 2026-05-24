// SPDX-FileCopyrightText: 2020 k0s authors
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"

	"github.com/k0sproject/k0s/internal/pkg/file"
	"github.com/k0sproject/k0s/internal/pkg/stringslice"
	"github.com/k0sproject/k0s/internal/pkg/users"
	"github.com/k0sproject/k0s/pkg/apis/k0s/v1beta1"
	"github.com/k0sproject/k0s/pkg/certificate"
	"github.com/k0sproject/k0s/pkg/config"
	"github.com/k0sproject/k0s/pkg/constant"
	"github.com/k0sproject/k0s/pkg/kubernetes"

	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
)

// Certificates is the Component implementation to manage all k0s certs
type Certificates struct {
	ClusterSpec *v1beta1.ClusterSpec
	K0sVars     *config.CfgVars
}

// Init initializes the certificate component
func (c *Certificates) Init(ctx context.Context) error {
	eg, _ := errgroup.WithContext(ctx)

	certManager := certificate.NewManager(c.K0sVars.CertRootDir)
	ca, err := certManager.EnsureCA("ca", "kubernetes-ca", c.ClusterSpec.API.CA.ExpiresAfter.Duration)
	if err != nil {
		return err
	}

	// We need CA cert loaded to generate client configs
	logrus.Debugf("CA key and cert exists, loading")
	caCertData, err := os.ReadFile(filepath.Join(c.K0sVars.CertRootDir, "ca.crt"))
	if err != nil {
		return fmt.Errorf("failed to read ca cert: %w", err)
	}
	kubeConfigAPIUrl := c.ClusterSpec.API.LocalURL()

	apiServerUID, err := users.LookupUID(constant.ApiserverUser)
	if err != nil {
		err = fmt.Errorf("failed to lookup UID for %q: %w", constant.ApiserverUser, err)
		apiServerUID = users.RootUID
		logrus.WithError(err).Warn("Files with key material for kube-apiserver user will be owned by root")
	}
	eg.Go(func() error {
		// Front proxy CA
		ca, err := certManager.EnsureCA("front-proxy-ca", "kubernetes-front-proxy-ca", c.ClusterSpec.API.CA.ExpiresAfter.Duration)
		if err != nil {
			return err
		}

		proxyClientReq := certificate.Request{
			Name: "front-proxy-client",
			CN:   "front-proxy-client",
			O:    "front-proxy-client",
			CA:   ca,
		}
		_, err = certManager.EnsureCertificate(proxyClientReq, apiServerUID, c.ClusterSpec.API.CA.CertificatesExpireAfter.Duration)

		return err
	})

	eg.Go(func() error {
		// admin cert & kubeconfig
		adminReq := certificate.Request{
			Name: "admin",
			CN:   "kubernetes-admin",
			O:    "system:masters",
			CA:   ca,
		}
		adminCert, err := certManager.EnsureCertificate(adminReq, users.RootUID, c.ClusterSpec.API.CA.CertificatesExpireAfter.Duration)
		if err != nil {
			return err
		}

		if err := kubeConfig(c.K0sVars.AdminKubeConfigPath, kubeConfigAPIUrl, caCertData, "admin", &adminCert, users.RootUID, constant.OwnerOnlyMode); err != nil {
			return err
		}

		return certManager.CreateKeyPair("sa", apiServerUID)
	})

	eg.Go(func() error {
		// konnectivity kubeconfig
		konnectivityReq := certificate.Request{
			Name: "konnectivity",
			CN:   "kubernetes-konnectivity",
			O:    "system:masters", // TODO: We need to figure out if konnectivity really needs superpowers
			CA:   ca,
		}

		uid, err := users.LookupUID(constant.KonnectivityServerUser)
		if err != nil {
			err = fmt.Errorf("failed to lookup UID for %q: %w", constant.KonnectivityServerUser, err)
			uid = users.RootUID
			logrus.WithError(err).Warn("Files with key material for konnectivity-server user will be owned by root")
		}

		konnectivityCert, err := certManager.EnsureCertificate(konnectivityReq, uid, c.ClusterSpec.API.CA.CertificatesExpireAfter.Duration)
		if err != nil {
			return err
		}

		return kubeConfig(c.K0sVars.KonnectivityKubeConfigPath, kubeConfigAPIUrl, caCertData, "konnectivity", &konnectivityCert, uid, constant.CertSecureMode)
	})

	eg.Go(func() error {
		ccmReq := certificate.Request{
			Name: "ccm",
			CN:   "system:kube-controller-manager",
			O:    "system:kube-controller-manager",
			CA:   ca,
		}
		ccmCert, err := certManager.EnsureCertificate(ccmReq, apiServerUID, c.ClusterSpec.API.CA.CertificatesExpireAfter.Duration)
		if err != nil {
			return err
		}

		return kubeConfig(filepath.Join(c.K0sVars.CertRootDir, "ccm.conf"), kubeConfigAPIUrl, caCertData, "kube-controller-manager", &ccmCert, apiServerUID, constant.OwnerOnlyMode)
	})

	eg.Go(func() error {
		schedulerReq := certificate.Request{
			Name: "scheduler",
			CN:   "system:kube-scheduler",
			O:    "system:kube-scheduler",
			CA:   ca,
		}

		uid, err := users.LookupUID(constant.SchedulerUser)
		if err != nil {
			err = fmt.Errorf("failed to lookup UID for %q: %w", constant.SchedulerUser, err)
			uid = users.RootUID
			logrus.WithError(err).Warn("Files with key material for kube-scheduler user will be owned by root")
		}

		schedulerCert, err := certManager.EnsureCertificate(schedulerReq, uid, c.ClusterSpec.API.CA.CertificatesExpireAfter.Duration)
		if err != nil {
			return err
		}

		return kubeConfig(filepath.Join(c.K0sVars.CertRootDir, "scheduler.conf"), kubeConfigAPIUrl, caCertData, "kube-scheduler", &schedulerCert, uid, constant.OwnerOnlyMode)
	})

	eg.Go(func() error {
		kubeletClientReq := certificate.Request{
			Name: "apiserver-kubelet-client",
			CN:   "apiserver-kubelet-client",
			O:    "system:masters",
			CA:   ca,
		}
		_, err := certManager.EnsureCertificate(kubeletClientReq, apiServerUID, c.ClusterSpec.API.CA.CertificatesExpireAfter.Duration)
		return err
	})

	hostnames, err := c.generateSANList(ctx)
	if err != nil {
		return fmt.Errorf("failed to generate SAN list: %w", err)
	}

	eg.Go(func() error {
		serverReq := certificate.Request{
			Name:      "server",
			CN:        "kubernetes",
			O:         "kubernetes",
			CA:        ca,
			Hostnames: hostnames,
		}
		_, err = certManager.EnsureCertificate(serverReq, apiServerUID, c.ClusterSpec.API.CA.CertificatesExpireAfter.Duration)
		return err
	})

	eg.Go(func() error {
		apiReq := certificate.Request{
			Name:      "k0s-api",
			CN:        "k0s-api",
			O:         "kubernetes",
			CA:        ca,
			Hostnames: hostnames,
		}
		// TODO Not sure about the user...
		_, err := certManager.EnsureCertificate(apiReq, apiServerUID, c.ClusterSpec.API.CA.CertificatesExpireAfter.Duration)
		return err
	})

	return eg.Wait()
}

func (c *Certificates) generateSANList(ctx context.Context) ([]string, error) {
	hostnames := []string{
		"kubernetes",
		"kubernetes.default",
		"kubernetes.default.svc",
		"kubernetes.default.svc.cluster",
		"kubernetes.svc." + c.ClusterSpec.Network.ClusterDomain,
		"localhost",
		"127.0.0.1",
	}

	if externalHost := c.ClusterSpec.API.ExternalHost(); externalHost != "" {
		hostnames = append(hostnames, externalHost)
	}
	hostnames = append(hostnames, c.ClusterSpec.API.Address)
	hostnames = append(hostnames, c.ClusterSpec.API.SANs...)

	if localIPs, err := detectLocalIPs(ctx); err != nil {
		return nil, fmt.Errorf("error detecting local IP: %w", err)
	} else {
		hostnames = append(hostnames, localIPs...)
	}

	// Add to SANs the IPs from the control plane load balancer
	cplb := c.ClusterSpec.Network.ControlPlaneLoadBalancing
	if cplb != nil && cplb.Enabled && cplb.Keepalived != nil {
		for _, v := range cplb.Keepalived.VRRPInstances {
			for _, vip := range v.VirtualIPs {
				ip, _, err := net.ParseCIDR(vip)
				if err != nil {
					return nil, fmt.Errorf("error parsing virtualIP %s: %w", vip, err)
				}
				hostnames = append(hostnames, ip.String())
			}
		}
	}

	internalAPIAddress, err := c.ClusterSpec.Network.InternalAPIAddresses()
	if err != nil {
		return nil, err
	}
	hostnames = append(hostnames, internalAPIAddress...)

	return stringslice.Unique(hostnames), nil
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

	ifaceAddrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil, fmt.Errorf("failed to list network interfaces: %w", err)
	}

	for _, a := range ifaceAddrs {
		if ipnet, ok := a.(*net.IPNet); ok {
			if ipnet.IP.To4() != nil || ipnet.IP.To16() != nil {
				localIPs = append(localIPs, ipnet.IP.String())
			}
		}
	}

	return localIPs, nil
}

func kubeConfig(dest string, url *url.URL, caCertData []byte, userName string, clientCert *certificate.Certificate, ownerID int, fileMode os.FileMode) error {
	// We always overwrite the kubeconfigs as the certs might be regenerated at startup
	kubeconfig, err := clientcmd.Write(kubernetes.KubeConfig(
		"local", &clientcmdapi.Cluster{
			Server:                   url.String(),
			CertificateAuthorityData: caCertData,
		},
		userName, &clientcmdapi.AuthInfo{
			ClientCertificateData: clientCert.Cert,
			ClientKeyData:         clientCert.Key,
		},
	))
	if err != nil {
		return err
	}

	return file.AtomicWithTarget(dest).WithPermissions(fileMode).WithOwner(ownerID).Write(kubeconfig)
}
