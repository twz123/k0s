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

package constant

import (
	"crypto/tls"
	"strings"
)

// Network providers
const (
	CNIProviderCalico     = "calico"
	CNIProviderKubeRouter = "kuberouter"
)

const (

	// DataDirMode is the expected directory permissions for DataDirDefault
	DataDirMode = 0755
	// EtcdDataDirMode is the expected directory permissions for EtcdDataDir. see https://docs.datadoghq.com/security_monitoring/default_rules/cis-kubernetes-1.5.1-1.1.11/
	EtcdDataDirMode = 0700
	// CertRootDirMode is the expected directory permissions for CertRootDir.
	CertRootDirMode = 0751
	// EtcdCertDirMode is the expected directory permissions for EtcdCertDir
	EtcdCertDirMode = 0711
	// CertMode is the expected permissions for certificates. see: https://docs.datadoghq.com/security_monitoring/default_rules/cis-kubernetes-1.5.1-1.1.20/
	CertMode = 0644
	// CertSecureMode is the expected file permissions for secure files. see: https://docs.datadoghq.com/security_monitoring/default_rules/cis-kubernetes-1.5.1-1.1.13/
	// this relates to files like: admin.conf, kube-apiserver.yaml, certificate files, and more
	CertSecureMode = 0640
	// BinDirMode is the expected directory permissions for BinDir
	BinDirMode = 0755
	// RunDirMode is the expected permissions of RunDir
	RunDirMode = 0755
	// PidFileMode is the expected file permissions for pid files
	PidFileMode = 0644
	// ManifestsDirMode is the expected directory permissions for ManifestsDir
	ManifestsDirMode = 0755
	// KineDBDirMode is the expected directory permissions for the Kine DB
	KineDBDirMode = 0750

	/* User accounts for services */

	// EtcdUser defines the user to use for running etcd process
	EtcdUser = "etcd"
	// KineUser defines the user to use for running kine process
	KineUser = "kube-apiserver" // apiserver needs to be able to read the kine unix socket
	// ApiserverUser defines the user to use for running k8s api-server process
	ApiserverUser = "kube-apiserver"
	// SchedulerUser defines the user to use for running k8s scheduler
	SchedulerUser = "kube-scheduler"
	// KonnectivityServerUser deinfes the user to use for konnectivity-server
	KonnectivityServerUser = "konnectivity-server"

	// KubernetesMajorMinorVersion defines the current embedded major.minor version info
	KubernetesMajorMinorVersion = "1.28"

	/* Image Constants */

	KonnectivityImage                  = "quay.io/twz123/apiserver-network-proxy-agent"
	KonnectivityImageVersion           = "v0.1.4-1@sha256:66a0ce4a1b7f98ea74510d30c1e96d80846c9a233b9c6eb30143d32209e127a3"
	PushGatewayImage                   = "quay.io/k0sproject/pushgateway-ttl"
	PushGatewayImageVersion            = "1.4.0-k0s.0"
	MetricsImage                       = "quay.io/twz123/metrics-server"
	MetricsImageVersion                = "v0.6.4-1@sha256:ee0d5a55b6724d4a955aaa5357d655092f4e4d1458f92e1b5d79d9fd127073d0"
	KubeProxyImage                     = "quay.io/twz123/kube-proxy"
	KubeProxyImageVersion              = "v1.28.3-1@sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	CoreDNSImage                       = "quay.io/twz123/coredns"
	CoreDNSImageVersion                = "1.11.1-1@sha256:ef304af35da98ff9f1af445b103b3fd73221ffaddb802be152cd0c488ec19699"
	EnvoyProxyImage                    = "quay.io/k0sproject/envoy-distroless"
	EnvoyProxyImageVersion             = "v1.24.1"
	CalicoImage                        = "quay.io/k0sproject/calico-cni"
	CalicoComponentImagesVersion       = "v3.26.1-1"
	CalicoNodeImage                    = "quay.io/k0sproject/calico-node"
	KubeControllerImage                = "quay.io/k0sproject/calico-kube-controllers"
	KubeRouterCNIImage                 = "quay.io/twz123/kube-router"
	KubeRouterCNIImageVersion          = "v1.6.0-iptables1.8.9-1@sha256:7ddbda29726da778945274ede6ff530351c6075695779777486d6ecc5ce8ea58"
	KubeRouterCNIInstallerImage        = "quay.io/twz123/cni-node"
	KubeRouterCNIInstallerImageVersion = "1.3.0-k0s.1@sha256:c08c83a7388bd3d92637846603d7065871b2c7e59f4a0de1e701c1045a1215ea"
	OpenEBSRepository                  = "https://openebs.github.io/charts"
	OpenEBSVersion                     = "3.3.0"

	/* Controller component names */

	APIConfigComponentName             = "api-config" // Deprecated: just don't use dynamic config
	APIEndpointReconcilerComponentName = "endpoint-reconciler"
	ControlAPIComponentName            = "control-api"
	CoreDNSComponentname               = "coredns"
	CsrApproverComponentName           = "csr-approver"
	HelmComponentName                  = "helm"
	KonnectivityServerComponentName    = "konnectivity-server"
	KubeControllerManagerComponentName = "kube-controller-manager"
	KubeProxyComponentName             = "kube-proxy"
	KubeSchedulerComponentName         = "kube-scheduler"
	KubeletConfigComponentName         = "kubelet-config" // Deprecated: replaced by worker-config
	WorkerConfigComponentName          = "worker-config"
	MetricsServerComponentName         = "metrics-server"
	NetworkProviderComponentName       = "network-provider"
	SystemRbacComponentName            = "system-rbac"
	NodeRoleComponentName              = "node-role"
	WindowsNodeComponentName           = "windows-node-role"
	AutopilotComponentName             = "autopilot"

	// ClusterConfigNamespace is the namespace where we expect to find the ClusterConfig CRs
	ClusterConfigNamespace  = "kube-system"
	ClusterConfigObjectName = "k0s"

	NodeRoleLabelNamespace = "node-role.kubernetes.io"
	K0SNodeRoleLabel       = "node.k0sproject.io/role"
)

// The list of allowed TLS v1.2 cipher suites. Those should be used for k0s
// itself and all embedded components. Note that TLS v1.3 ciphers are currently
// not configurable in Go.
//
// https://ssl-config.mozilla.org/#server=go&config=intermediate
var AllowedTLS12CipherSuiteIDs = []uint16{
	tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
	tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
	tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256,
	tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
	tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
	tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305_SHA256,
}

// A comma-separated string version of [AllowedTLS12CipherSuiteIDs], suitable to
// be used as CLI arg for binaries.
func AllowedTLS12CipherSuiteNames() string {
	var cipherSuites strings.Builder
	for i, cipherSuite := range AllowedTLS12CipherSuiteIDs {
		if i > 0 {
			cipherSuites.WriteRune(',')
		}
		cipherSuites.WriteString(tls.CipherSuiteName(cipherSuite))
	}
	return cipherSuites.String()
}
