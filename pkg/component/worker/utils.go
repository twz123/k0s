/*
Copyright 2021 k0s authors

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

package worker

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"time"

	"github.com/k0sproject/k0s/internal/pkg/dir"
	"github.com/k0sproject/k0s/internal/pkg/file"
	"github.com/k0sproject/k0s/pkg/config"
	"github.com/k0sproject/k0s/pkg/constant"
	token "github.com/k0sproject/k0s/pkg/join"

	apitypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/kubernetes/pkg/kubelet/certificate/bootstrap"

	"github.com/avast/retry-go"
	"github.com/sirupsen/logrus"
)

func BootstrapKubeletKubeconfig(ctx context.Context, k0sVars *config.CfgVars, nodeName apitypes.NodeName, workerOpts *config.WorkerOptions) error {
	bootstrapKubeconfigPath := filepath.Join(k0sVars.DataDir, "kubelet-bootstrap.conf")

	// When using `k0s install` along with a join token, that join token
	// argument will be registered within the k0s service definition and be
	// passed to k0s each time it gets started as a service. Hence that token
	// needs to be ignored if it has already been used. This results in the
	// following order of precedence:

	var bootstrapKubeconfig *clientcmdapi.Config
	switch {
	// 1: Regular kubelet kubeconfig file exists.
	// The kubelet kubeconfig has been bootstrapped already.
	case file.Exists(k0sVars.KubeletAuthConfigPath):
		return nil

	// 2: Kubelet bootstrap kubeconfig file exists.
	// The kubelet kubeconfig will be bootstrapped without a join token.
	case file.Exists(bootstrapKubeconfigPath):
		var err error
		bootstrapKubeconfig, err = clientcmd.LoadFromFile(bootstrapKubeconfigPath)
		if err != nil {
			return fmt.Errorf("failed to parse kubelet bootstrap kubeconfig from file: %w", err)
		}

	// 3: A join token has been given.
	// Bootstrap the kubelet kubeconfig via the embedded bootstrap config.
	case workerOpts.TokenArg != "" || workerOpts.TokenFile != "":
		var tokenData string
		if workerOpts.TokenArg != "" {
			tokenData = workerOpts.TokenArg
		} else {
			var problem string
			data, err := os.ReadFile(workerOpts.TokenFile)
			if errors.Is(err, os.ErrNotExist) {
				problem = "not found"
			} else if err != nil {
				return fmt.Errorf("failed to read token file: %w", err)
			} else if len(data) == 0 {
				problem = "is empty"
			}
			if problem != "" {
				return fmt.Errorf("token file %q %s"+
					`: obtain a new token via "k0s token create ..." and store it in the file`+
					` or reinstall this node via "k0s install --force ..." or "k0sctl apply --force ..."`,
					workerOpts.TokenFile, problem)
			}

			tokenData = string(data)
		}

		// Join token given, so use that.
		kubeconfig, err := token.DecodeJoinToken(tokenData)
		if err != nil {
			return fmt.Errorf("failed to decode join token: %w", err)
		}

		// Load the bootstrap kubeconfig to validate it.
		bootstrapKubeconfig, err = clientcmd.Load(kubeconfig)
		if err != nil {
			return fmt.Errorf("failed to parse kubelet bootstrap kubeconfig from join token: %w", err)
		}

		// Write the kubelet bootstrap kubeconfig to a temporary file, as the
		// kubelet bootstrap API only accepts files.
		bootstrapKubeconfigPath, err = writeKubeletBootstrapKubeconfig(kubeconfig)
		if err != nil {
			return fmt.Errorf("failed to write kubelet bootstrap kubeconfig: %w", err)
		}

		// Ensure that the temporary kubelet bootstrap kubeconfig file will be
		// removed when done.
		defer func() {
			if err := os.Remove(bootstrapKubeconfigPath); err != nil && !os.IsNotExist(err) {
				logrus.WithError(err).Error("Failed to remove kubelet bootstrap kubeconfig file")
			}
		}()

		logrus.Debug("Wrote kubelet bootstrap kubeconfig file: ", bootstrapKubeconfigPath)

	// 4: None of the above, bail out.
	default:
		return errors.New("neither regular nor bootstrap kubelet kubeconfig files exist and no join token given; dunno how to make kubelet authenticate to API server")
	}

	kubeletCAPath := path.Join(k0sVars.CertRootDir, "ca.crt")
	if !file.Exists(kubeletCAPath) {
		if err := dir.Init(k0sVars.CertRootDir, constant.CertRootDirMode); err != nil {
			return fmt.Errorf("failed to initialize directory '%s': %w", k0sVars.CertRootDir, err)
		}
		err := file.WriteContentAtomically(kubeletCAPath, bootstrapKubeconfig.Clusters["k0s"].CertificateAuthorityData, constant.CertMode)
		if err != nil {
			return fmt.Errorf("failed to write ca client cert: %w", err)
		}
	}

	if tokenType := token.GetTokenType(bootstrapKubeconfig); tokenType != "kubelet-bootstrap" {
		return fmt.Errorf("wrong token type %s, expected type: kubelet-bootstrap", tokenType)
	}

	certDir := filepath.Join(k0sVars.KubeletRootDir, "pki")
	if err := dir.Init(certDir, constant.DataDirMode); err != nil {
		return fmt.Errorf("failed to initialize kubelet certificate directory: %w", err)
	}

	logrus.Infof("Bootstrapping kubelet client configuration using %s as node name", nodeName)

	if err := retry.Do(
		func() error {
			return bootstrap.LoadClientCert(
				ctx,
				k0sVars.KubeletAuthConfigPath,
				bootstrapKubeconfigPath,
				certDir,
				apitypes.NodeName(nodeName),
			)
		},
		retry.Context(ctx),
		retry.LastErrorOnly(true),
		retry.Delay(1*time.Second),
		retry.OnRetry(func(attempt uint, err error) {
			logrus.WithError(err).Debugf("Failed to bootstrap kubelet client configuration in attempt #%d, retrying after backoff", attempt+1)
		}),
	); err != nil {
		return fmt.Errorf("failed to bootstrap kubelet client configuration: %w", err)
	}

	logrus.Debug("Successfully bootstrapped kubelet client configuration")
	return nil
}

func writeKubeletBootstrapKubeconfig(kubeconfig []byte) (string, error) {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" && runtime.GOOS != "windows" {
		dir = "/run"
	}

	bootstrapFile, err := os.CreateTemp(dir, "k0s-*-kubelet-bootstrap-kubeconfig")
	if err != nil {
		return "", err
	}

	_, writeErr := bootstrapFile.Write(kubeconfig)
	closeErr := bootstrapFile.Close()

	if writeErr != nil || closeErr != nil {
		rmErr := os.Remove(bootstrapFile.Name())
		// Don't propagate any fs.ErrNotExist errors. There is no point in doing
		// this, since the desired state is already reached: The bootstrap file
		// is no longer present on the file system.
		if errors.Is(rmErr, fs.ErrNotExist) {
			rmErr = nil
		}

		return "", errors.Join(writeErr, closeErr, rmErr)
	}

	return bootstrapFile.Name(), nil
}
