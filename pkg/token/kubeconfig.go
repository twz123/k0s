// SPDX-FileCopyrightText: 2021 k0s authors
// SPDX-License-Identifier: Apache-2.0

package token

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/k0sproject/k0s/internal/secret"
	"github.com/k0sproject/k0s/pkg/apis/k0s/v1beta1"
	"github.com/k0sproject/k0s/pkg/config"

	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	bootstraptokenv1 "k8s.io/kubernetes/cmd/kubeadm/app/apis/bootstraptoken/v1"
)

const (
	RoleController = "controller"
	RoleWorker     = "worker"
)

const (
	ControllerTokenAuthName = "controller-bootstrap"
	WorkerTokenAuthName     = "kubelet-bootstrap"
)

// CreateKubeletBootstrapToken creates a new k0s bootstrap token.
func CreateKubeletBootstrapToken(ctx context.Context, api *v1beta1.APISpec, k0sVars *config.CfgVars, role string, expiry time.Duration) (JoinToken, error) {
	userName, joinURL, err := loadUserAndJoinURL(api, role)
	if err != nil {
		return JoinToken{}, err
	}

	caCert, err := loadCACert(k0sVars)
	if err != nil {
		return JoinToken{}, err
	}

	token, err := loadToken(ctx, k0sVars, role, expiry)
	if err != nil {
		return JoinToken{}, err
	}

	return GenerateJoinToken(joinURL, caCert, userName, token)
}

// GenerateJoinToken generates a join token for the given join URL: a
// kubeconfig for the given user, authenticated by the given bootstrap token.
func GenerateJoinToken(joinURL string, caCert []byte, userName string, token *bootstraptokenv1.BootstrapTokenString) (JoinToken, error) {
	const k0sContextName = "k0s"
	kubeconfig, err := clientcmd.Write(clientcmdapi.Config{
		Clusters: map[string]*clientcmdapi.Cluster{k0sContextName: {
			Server:                   joinURL,
			CertificateAuthorityData: caCert,
		}},
		Contexts: map[string]*clientcmdapi.Context{k0sContextName: {
			Cluster:  k0sContextName,
			AuthInfo: userName,
		}},
		CurrentContext: k0sContextName,
		AuthInfos: map[string]*clientcmdapi.AuthInfo{userName: {
			Token: token.String(),
		}},
	})
	if err != nil {
		return JoinToken{}, err
	}
	return JoinToken{secret.From[JoinToken](kubeconfig)}, nil
}

func loadUserAndJoinURL(api *v1beta1.APISpec, role string) (string, string, error) {
	switch role {
	case RoleController:
		return ControllerTokenAuthName, api.K0sControlPlaneAPIAddress(), nil
	case RoleWorker:
		return WorkerTokenAuthName, api.APIAddressURL(), nil
	default:
		return "", "", fmt.Errorf("unsupported role %q; supported roles are %q and %q", role, RoleController, RoleWorker)
	}
}

func loadCACert(k0sVars *config.CfgVars) ([]byte, error) {
	crtFile := filepath.Join(k0sVars.CertRootDir, "ca.crt")
	caCert, err := os.ReadFile(crtFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read cluster CA from %q: %w; check if the control plane is initialized on this node", crtFile, err)
	}

	return caCert, nil
}

func loadToken(ctx context.Context, k0sVars *config.CfgVars, role string, expiry time.Duration) (*bootstraptokenv1.BootstrapTokenString, error) {
	manager, err := NewManager(k0sVars.AdminKubeConfigPath)
	if err != nil {
		return nil, err
	}
	return manager.Create(ctx, expiry, role)
}
