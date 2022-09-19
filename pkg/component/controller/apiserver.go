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
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/sirupsen/logrus"

	"github.com/k0sproject/k0s/internal/pkg/templatewriter"
	"github.com/k0sproject/k0s/internal/pkg/users"
	"github.com/k0sproject/k0s/pkg/apis/k0s.k0sproject.io/v1beta1"
	"github.com/k0sproject/k0s/pkg/assets"
	"github.com/k0sproject/k0s/pkg/component"
	"github.com/k0sproject/k0s/pkg/config"
	"github.com/k0sproject/k0s/pkg/constant"
	"github.com/k0sproject/k0s/pkg/etcd"
	"github.com/k0sproject/k0s/pkg/supervisor"
)

// APIServer implement the component interface to run kube api
type APIServer struct {
	K0sVars            constant.CfgVars
	ControlPlane       config.ControlPlaneSpec
	LogLevel           string
	EnableKonnectivity bool

	supervisor supervisor.Supervisor
	uid, gid   int
}

var _ component.Component = (*APIServer)(nil)
var _ component.Healthz = (*APIServer)(nil)

var apiDefaultArgs = map[string]string{
	"allow-privileged":                   "true",
	"requestheader-extra-headers-prefix": "X-Remote-Extra-",
	"requestheader-group-headers":        "X-Remote-Group",
	"requestheader-username-headers":     "X-Remote-User",
	"secure-port":                        "6443",
	"anonymous-auth":                     "false",
	"tls-cipher-suites":                  "TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384",
}

const egressSelectorConfigTemplate = `
apiVersion: apiserver.k8s.io/v1beta1
kind: EgressSelectorConfiguration
egressSelections:
- name: cluster
  connection:
    proxyProtocol: GRPC
    transport:
      uds:
        udsName: {{ .UDSName }}
`

type egressSelectorConfig struct {
	UDSName string
}

// Init extracts needed binaries
func (a *APIServer) Init(_ context.Context) error {
	var err error
	a.uid, err = users.GetUID(constant.ApiserverUser)
	if err != nil {
		logrus.Warning(fmt.Errorf("running kube-apiserver as root: %w", err))
	}
	return assets.Stage(a.K0sVars.BinDir, "kube-apiserver", constant.BinDirMode)
}

// Run runs kube api
func (a *APIServer) Start(context.Context) error {
	var serviceClusterIPRange strings.Builder
	if a.ControlPlane.Network.ServiceCIDRs.V4 != nil {
		serviceClusterIPRange.WriteString(a.ControlPlane.Network.ServiceCIDRs.V4.String())
	}
	if a.ControlPlane.Network.ServiceCIDRs.V6 != nil {
		if serviceClusterIPRange.Len() > 0 {
			serviceClusterIPRange.WriteRune(',')
		}
		serviceClusterIPRange.WriteString(a.ControlPlane.Network.ServiceCIDRs.V6.String())
	}

	logrus.Info("Starting kube-apiserver")
	args := map[string]string{
		"advertise-address":                a.ControlPlane.APIServer.BindAddress.IP.String(),
		"secure-port":                      a.ControlPlane.APIServer.BindAddress.Port.String(),
		"authorization-mode":               "Node,RBAC",
		"client-ca-file":                   filepath.Join(a.K0sVars.CertRootDir, "ca.crt"),
		"enable-bootstrap-token-auth":      "true",
		"kubelet-client-certificate":       filepath.Join(a.K0sVars.CertRootDir, "apiserver-kubelet-client.crt"),
		"kubelet-client-key":               filepath.Join(a.K0sVars.CertRootDir, "apiserver-kubelet-client.key"),
		"kubelet-preferred-address-types":  "InternalIP,ExternalIP,Hostname",
		"proxy-client-cert-file":           filepath.Join(a.K0sVars.CertRootDir, "front-proxy-client.crt"),
		"proxy-client-key-file":            filepath.Join(a.K0sVars.CertRootDir, "front-proxy-client.key"),
		"requestheader-allowed-names":      "front-proxy-client",
		"requestheader-client-ca-file":     filepath.Join(a.K0sVars.CertRootDir, "front-proxy-ca.crt"),
		"service-account-key-file":         filepath.Join(a.K0sVars.CertRootDir, "sa.pub"),
		"service-cluster-ip-range":         toServiceClusterIPRange(&a.ControlPlane.Network.ServiceCIDRs),
		"tls-cert-file":                    filepath.Join(a.K0sVars.CertRootDir, "server.crt"),
		"tls-private-key-file":             filepath.Join(a.K0sVars.CertRootDir, "server.key"),
		"service-account-signing-key-file": filepath.Join(a.K0sVars.CertRootDir, "sa.key"),
		"service-account-issuer":           "https://kubernetes.default.svc",
		"service-account-jwks-uri":         "https://kubernetes.default.svc/openid/v1/jwks",
		"profiling":                        "false",
		"v":                                a.LogLevel,
		"kubelet-certificate-authority":    filepath.Join(a.K0sVars.CertRootDir, "ca.crt"),
		"enable-admission-plugins":         "NodeRestriction",
	}

	apiAudiences := []string{"https://kubernetes.default.svc"}

	if a.EnableKonnectivity {
		err := a.writeKonnectivityConfig()
		if err != nil {
			return err
		}
		args["egress-selector-config-file"] = filepath.Join(a.K0sVars.DataDir, "konnectivity.conf")
		apiAudiences = append(apiAudiences, "system:konnectivity-server")
	}

	args["api-audiences"] = strings.Join(apiAudiences, ",")

	for name, value := range a.ControlPlane.APIServer.ExtraArgs {
		if arg, ok := args[name]; ok {
			logrus.Warnf("overriding apiserver flag %q with user provided value %q", name+"="+arg, value)
		}
		args[name] = value
	}

	args = v1beta1.EnableFeatureGate(args, v1beta1.ServiceInternalTrafficPolicyFeatureGate)
	for name, value := range apiDefaultArgs {
		if args[name] == "" {
			args[name] = value
		}
	}
	if a.ControlPlane.APIServer.ExternalAddress != "" || a.ControlPlane.APIServer.TunneledNetworkingMode {
		args["endpoint-reconciler-type"] = "none"
	}

	if err := a.collectEtcdArgs(args); err != nil {
		return err
	}

	var apiServerArgs []string
	for name, value := range args {
		apiServerArgs = append(apiServerArgs, fmt.Sprintf("--%s=%s", name, value))
	}

	a.supervisor = supervisor.Supervisor{
		Name:    "kube-apiserver",
		BinPath: assets.BinPath("kube-apiserver", a.K0sVars.BinDir),
		RunDir:  a.K0sVars.RunDir,
		DataDir: a.K0sVars.DataDir,
		Args:    apiServerArgs,
		UID:     a.uid,
		GID:     a.gid,
	}

	return a.supervisor.Supervise()
}

func (a *APIServer) writeKonnectivityConfig() error {
	tw := templatewriter.TemplateWriter{
		Name:     "konnectivity",
		Template: egressSelectorConfigTemplate,
		Data: egressSelectorConfig{
			UDSName: filepath.Join(a.K0sVars.KonnectivitySocketDir, "konnectivity-server.sock"),
		},
		Path: filepath.Join(a.K0sVars.DataDir, "konnectivity.conf"),
	}
	err := tw.Write()
	if err != nil {
		return fmt.Errorf("failed to write konnectivity config: %w", err)
	}

	return nil
}

// Stop stops APIServer
func (a *APIServer) Stop() error {
	return a.supervisor.Stop()
}

// Health-check interface
func (a *APIServer) Healthy() error {
	// Load client cert so the api can authenitcate the request.
	certFile := filepath.Join(a.K0sVars.CertRootDir, "admin.crt")
	keyFile := filepath.Join(a.K0sVars.CertRootDir, "admin.key")
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return err
	}
	// Load CA cert
	caCert, err := os.ReadFile(filepath.Join(a.K0sVars.CertRootDir, "ca.crt"))
	if err != nil {
		return err
	}
	caCertPool := x509.NewCertPool()
	caCertPool.AppendCertsFromPEM(caCert)
	// Setup HTTPS client
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      caCertPool,
	}
	tr := &http.Transport{
		TLSClientConfig: tlsConfig,
	}
	client := &http.Client{Transport: tr}

	resp, err := client.Get((&url.URL{
		Scheme:   "https",
		Host:     a.ControlPlane.APIServer.BindAddress.String(),
		Path:     "/readyz",
		RawQuery: "verbose",
	}).String())
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, err := io.ReadAll(resp.Body)
		if err == nil {
			logrus.Debug("API server readyz output:\n ", string(body))
		}
		return fmt.Errorf("expected 200 for API server readyz check, got %d", resp.StatusCode)
	}
	return nil
}

func (a *APIServer) collectEtcdArgs(args map[string]string) error {
	switch a.ControlPlane.Storage.Type {
	case config.EtcdStorageType:
		args["etcd-servers"] = a.ControlPlane.Storage.Etcd.URL().String()
		args["etcd-certfile"] = filepath.Join(a.K0sVars.CertRootDir, etcd.CertFile)
		args["etcd-keyfile"] = filepath.Join(a.K0sVars.CertRootDir, etcd.KeyFile)
		args["etcd-cafile"] = filepath.Join(a.K0sVars.EtcdCertDir, etcd.CAFile)
		return nil

	case config.ExternalEtcdStorageType:
		var etcdServers strings.Builder
		for _, url := range a.ControlPlane.Storage.ExternalEtcd.Endpoints {
			if etcdServers.Len() > 0 {
				etcdServers.WriteRune(',')
			}
			etcdServers.WriteString(url.String())
		}
		args["etcd-servers"] = etcdServers.String()
		args["etcd-prefix"] = a.ControlPlane.Storage.ExternalEtcd.EtcdPrefix
		if a.ControlPlane.Storage.ExternalEtcd.TLS != nil {
			args["etcd-cafile"] = a.ControlPlane.Storage.ExternalEtcd.TLS.CAFile
			args["etcd-certfile"] = a.ControlPlane.Storage.ExternalEtcd.TLS.CertFile
			args["etcd-keyfile"] = a.ControlPlane.Storage.ExternalEtcd.TLS.KeyFile
		}
		return nil

	case config.KineStorageType:
		args["etcd-servers"] = "unix://" + a.K0sVars.KineSocketPath // kine endpoint
		return nil
	}

	return fmt.Errorf("unsupported storage type %q", a.ControlPlane.Storage.Type)
}

func toServiceClusterIPRange(cidrs *config.CIDRSpec) string {
	var serviceClusterIPRange strings.Builder
	if cidrs.V4 != nil {
		serviceClusterIPRange.WriteString(cidrs.V4.String())
	}
	if cidrs.V6 != nil {
		if serviceClusterIPRange.Len() > 0 {
			serviceClusterIPRange.WriteRune(',')
		}
		serviceClusterIPRange.WriteString(cidrs.V6.String())
	}
	return serviceClusterIPRange.String()
}
