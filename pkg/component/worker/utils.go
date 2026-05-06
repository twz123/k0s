// SPDX-FileCopyrightText: 2021 k0s authors
// SPDX-License-Identifier: Apache-2.0

package worker

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/k0sproject/k0s/internal/pkg/file"
	k0snet "github.com/k0sproject/k0s/internal/pkg/net"
	"github.com/k0sproject/k0s/pkg/config"
	kubeutil "github.com/k0sproject/k0s/pkg/kubernetes"

	apitypes "k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/kubernetes/pkg/kubelet/certificate/bootstrap"

	"github.com/avast/retry-go"
	"github.com/sirupsen/logrus"
)

func BootstrapKubeletClientConfig(ctx context.Context, k0sVars *config.CfgVars, nodeName apitypes.NodeName, workerOpts *config.WorkerOptions, getBootstrapKubeconfig clientcmd.KubeconfigGetter) error {
	log := logrus.WithFields(logrus.Fields{"component": "bootstrap-kubelet", "node_name": nodeName})
	bootstrapKubeconfigPath := filepath.Join(k0sVars.DataDir, "kubelet-bootstrap.conf")

	// When using `k0s install` along with a join token, that join token
	// argument will be registered within the k0s service definition and be
	// passed to k0s each time it gets started as a service. Hence that token
	// needs to be ignored if it has already been used. This results in the
	// following order of precedence:

	switch {
	// 1: Regular kubelet kubeconfig file exists.
	// The kubelet kubeconfig has been bootstrapped already.
	case file.Exists(k0sVars.KubeletAuthConfigPath):
		return nil

	// 2: Kubelet bootstrap kubeconfig file exists.
	// The kubelet kubeconfig will be bootstrapped without a join token.
	case file.Exists(bootstrapKubeconfigPath):
		// Nothing to do here.

	// 3: A bootstrap kubeconfig can be created (usually via a join token).
	// Bootstrap the kubelet kubeconfig via a temporary bootstrap config file.
	case getBootstrapKubeconfig != nil:
		bootstrapKubeconfig, err := getBootstrapKubeconfig()
		if err != nil {
			return fmt.Errorf("failed to get bootstrap kubeconfig: %w", err)
		}

		// Write the kubelet bootstrap kubeconfig to a temporary file, as the
		// kubelet bootstrap API only accepts files.
		bootstrapKubeconfigPath, err = writeKubeletBootstrapKubeconfig(*bootstrapKubeconfig)
		if err != nil {
			return fmt.Errorf("failed to write bootstrap kubeconfig: %w", err)
		}

		// Ensure that the temporary kubelet bootstrap kubeconfig file will be
		// removed when done.
		defer func() {
			if err := os.Remove(bootstrapKubeconfigPath); err != nil && !os.IsNotExist(err) {
				log.WithError(err).Error("Failed to remove bootstrap kubeconfig file")
			}
		}()

		log.Debug("Wrote bootstrap kubeconfig file: ", bootstrapKubeconfigPath)

	// 4: None of the above, bail out.
	default:
		return errors.New("neither regular nor bootstrap kubeconfig files exist and no join token given; dunno how to make kubelet authenticate to API server")
	}

	log.Info("Bootstrapping client configuration")

	if err := retry.Do(
		func() error {
			return bootstrap.LoadClientCert(
				ctx,
				k0sVars.KubeletAuthConfigPath,
				bootstrapKubeconfigPath,
				filepath.Join(k0sVars.KubeletRootDir, "pki"),
				nodeName,
			)
		},
		retry.Context(ctx),
		retry.LastErrorOnly(true),
		retry.Delay(1*time.Second),
		retry.OnRetry(func(attempt uint, err error) {
			log.WithError(err).WithField("attempt", attempt+1).Debug("Failed to bootstrap client configuration, retrying after backoff")
		}),
	); err != nil {
		return fmt.Errorf("failed to bootstrap client configuration: %w", err)
	}

	log.Info("Successfully bootstrapped client configuration")
	return nil
}

func writeKubeletBootstrapKubeconfig(kubeconfig clientcmdapi.Config) (string, error) {
	if err := clientcmdapi.MinifyConfig(&kubeconfig); err != nil {
		return "", fmt.Errorf("failed to minify bootstrap kubeconfig: %w", err)
	}

	bytes, err := clientcmd.Write(kubeconfig)
	if err != nil {
		return "", err
	}

	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" && runtime.GOOS != "windows" {
		dir = "/run"
	}

	bootstrapFile, err := os.CreateTemp(dir, "k0s-*-kubelet-bootstrap-kubeconfig")
	if err != nil {
		return "", err
	}

	_, writeErr := bootstrapFile.Write(bytes)
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

// CreateDirectKubeletKubeconfig creates a kubelet kubeconfig that points directly to the local API
// server instead of using NLLB. This is used on controller+worker nodes where we want kubelet to
// connect directly to the local API server.
func CreateDirectKubeletKubeconfig(ctx context.Context, k0sVars *config.CfgVars, nodeName apitypes.NodeName) (string, error) {
	log := logrus.WithFields(logrus.Fields{"component": "bootstrap-kubelet", "node_name": nodeName})

	nodeConfig, err := k0sVars.NodeConfig()
	if err != nil {
		return "", fmt.Errorf("failed to load node config: %w", err)
	}

	apiSpec := nodeConfig.Spec.API

	// Determine the local API server address
	localAPIServerAddress := "127.0.0.1"
	if apiSpec.OnlyBindToAddress {
		// API server binds only to specific address, use that address with proper IPv6 bracketing
		localAPIServerAddress = apiSpec.Address
	} else {
		// API server binds to all interfaces, use localhost
		// Try to resolve localhost to get the appropriate loopback address (IPv4/IPv6)
		if loopbackIP, err := getLoopbackIP(ctx); err != nil {
			log.WithError(err).Warn("Failed to resolve localhost, falling back to ", localAPIServerAddress)
		} else {
			localAPIServerAddress = loopbackIP.String()
		}
	}

	localAPIServer, err := k0snet.NewHostPort(localAPIServerAddress, uint16(apiSpec.Port))
	if err != nil {
		return "", err
	}

	log.Debugf("Using direct local API server URL for kubelet: %s", localAPIServer)

	directKubeconfig, err := kubeutil.ReadKubeconfig(k0sVars.KubeletAuthConfigPath)
	if err != nil {
		return "", fmt.Errorf("failed to read kubeconfig: %w", err)
	}

	directKubeconfig, err = kubeutil.PatchKubeconfigServerAddress(directKubeconfig, *localAPIServer)
	if err != nil {
		return "", fmt.Errorf("failed to patch kubeconfig: %w", err)
	}

	directKubeconfigPath := filepath.Join(k0sVars.RunDir, "kubelet-direct.conf")
	if err := kubeutil.WriteKubeconfig(directKubeconfig, directKubeconfigPath); err != nil {
		return "", fmt.Errorf("failed to write kubeconfig file: %w", err)
	}

	log.Debugf("Wrote direct kubeconfig file: %s", directKubeconfigPath)
	return directKubeconfigPath, nil
}

// getLoopbackIP resolves localhost to get the appropriate loopback IP address (IPv4 or IPv6)
func getLoopbackIP(ctx context.Context) (net.IP, error) {
	localIPs, err := net.DefaultResolver.LookupIPAddr(ctx, "localhost")
	if err != nil {
		return nil, fmt.Errorf("failed to resolve localhost: %w", err)
	}

	for _, addr := range localIPs {
		if addr.IP.IsLoopback() {
			return addr.IP, nil
		}
	}

	return nil, fmt.Errorf("no loopback IPs found for localhost: %v", localIPs)
}
