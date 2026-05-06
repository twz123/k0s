// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package kubernetes

import (
	"fmt"
	"net/url"

	"github.com/k0sproject/k0s/internal/pkg/file"
	k0snet "github.com/k0sproject/k0s/internal/pkg/net"
	"github.com/k0sproject/k0s/pkg/constant"

	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// Writes kubeconfig atomically to path with appropriate permissions.
func WriteKubeconfig(kubeconfig *clientcmdapi.Config, path string) error {
	bytes, err := clientcmd.Write(*kubeconfig)
	if err != nil {
		return err
	}

	return file.WriteContentAtomically(path, bytes, constant.CertSecureMode)
}

// Returns a minified copy of kubeconfig with the API server address replaced.
func PatchKubeconfigServerAddress(kubeconfig *clientcmdapi.Config, server k0snet.HostPort) (*clientcmdapi.Config, error) {
	kubeconfig = kubeconfig.DeepCopy()
	if err := clientcmdapi.MinifyConfig(kubeconfig); err != nil {
		return nil, err
	}

	cluster := kubeconfig.Clusters[kubeconfig.Contexts[kubeconfig.CurrentContext].Cluster]
	clusterServer, err := url.Parse(cluster.Server)
	if err != nil {
		return nil, fmt.Errorf("invalid server: %w", err)
	}
	clusterServer.Host = server.String()
	cluster.Server = clusterServer.String()
	return kubeconfig, nil
}
