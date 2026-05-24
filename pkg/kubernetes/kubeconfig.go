// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package kubernetes

import (
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

func KubeConfig(clusterName string, cluster *clientcmdapi.Cluster, userName string, userAuth *clientcmdapi.AuthInfo) clientcmdapi.Config {
	contextName := clusterName + "-" + userName
	return KubeConfigContext(clusterName, cluster, userName, userAuth, contextName)
}

func KubeConfigContext(clusterName string, cluster *clientcmdapi.Cluster, userName string, userAuth *clientcmdapi.AuthInfo, contextName string) clientcmdapi.Config {
	return clientcmdapi.Config{
		Clusters: map[string]*clientcmdapi.Cluster{clusterName: cluster},
		Contexts: map[string]*clientcmdapi.Context{contextName: {
			Cluster:  clusterName,
			AuthInfo: userName,
		}},
		CurrentContext: contextName,
		AuthInfos:      map[string]*clientcmdapi.AuthInfo{userName: userAuth},
	}
}
