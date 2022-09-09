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

package config

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
	"time"

	"github.com/avast/retry-go"
	"github.com/imdario/mergo"
	"github.com/sirupsen/logrus"
	"golang.org/x/exp/slices"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/k0sproject/k0s/internal/pkg/iface"
	k0sv1beta1 "github.com/k0sproject/k0s/pkg/apis/k0s.k0sproject.io/clientset/typed/k0s.k0sproject.io/v1beta1"
	"github.com/k0sproject/k0s/pkg/apis/k0s.k0sproject.io/v1beta1"
	"github.com/k0sproject/k0s/pkg/constant"
)

var (
	resourceType = v1.TypeMeta{APIVersion: "k0s.k0sproject.io/v1beta1", Kind: "clusterconfigs"}
	getOpts      = v1.GetOptions{TypeMeta: resourceType}
)

// run a config-request from the API and wait until the API is up
func (rules *ClientConfigLoadingRules) getConfigFromAPI(ctx context.Context, client k0sv1beta1.K0sV1beta1Interface) (*v1beta1.ClusterConfig, error) {
	clusterConfigs := client.ClusterConfigs(constant.ClusterConfigNamespace)

	var cfg *v1beta1.ClusterConfig
	var err error
	var localIPs []net.IP

	logrus.Debug("Loading the currently active ClusterConfiguration")

	cfg, err = clusterConfigs.Get(ctx, "k0s", getOpts)

	retryErr := retry.Do(
		func() error {
			cfg, err = clusterConfigs.Get(ctx, "k0s", getOpts)
			if ip := isConnRefused(err); ip != nil {
				// Trying to connect to ourselves here? This is a fast path for
				// cases where dynamic configuration is enabled and k0s tries to
				// load the config from itself before the API server is started.

				if !ip.IsLoopback() && localIPs == nil {
					if ips, ipErr := iface.CollectAllIPs(func(ip net.IP) net.IP { return ip }); ipErr != nil {
						localIPs = []net.IP{}
					} else {
						localIPs = ips
					}
				}

				if ip.IsLoopback() || slices.IndexFunc(localIPs, ip.Equal) >= 0 {
					err = fmt.Errorf("failed to connect to API server via local address: %w", err)
					return retry.Unrecoverable(err)
				}
			}

			return err
		},
		retry.Context(ctx),
		retry.LastErrorOnly(true),
		retry.MaxDelay(1*time.Second),
		retry.OnRetry(func(attempt uint, err error) {
			logrus.WithError(err).Debugf("Failed to fetch the ClusterConfig from API in attempt #%d, retrying after backoff", attempt+1)
		}),
	)
	if err == nil {
		err = retryErr
	}

	if err != nil {
		return nil, fmt.Errorf("failed to fetch ClusterConfig from API: %w", err)
	}

	return cfg, nil
}

// when API config is enabled, but only node config is needed (for bootstrapping commands)
func (rules *ClientConfigLoadingRules) mergeNodeAndClusterconfig(nodeConfig *v1beta1.ClusterConfig, apiConfig *v1beta1.ClusterConfig) (*v1beta1.ClusterConfig, error) {
	// Get cluster config...
	clusterConfig := apiConfig.GetClusterWideConfig()
	// ...and overwrite any node-specific parts.
	err := mergo.Merge(clusterConfig, nodeConfig.GetNodeConfig(), mergo.WithOverride)
	if err != nil {
		return nil, err
	}

	return clusterConfig, nil
}

func isConnRefused(err error) net.IP {
	// https://github.com/golang/go/issues/45621
	// https://cs.opensource.google/go/x/sys/+/87db552b00fd1d5e9f6b1d3a845e5d711b98f7e2:windows/zerrors_windows.go;l=2693
	const WSAECONNREFUSED syscall.Errno = 10061 // == 0x274d

	var opErr *net.OpError
	if errors.As(err, &opErr) {
		var syscallErr *os.SyscallError
		if errors.As(opErr.Err, &syscallErr) &&
			syscallErr.Syscall == "connect" &&
			(syscallErr.Err == syscall.ECONNREFUSED || syscallErr.Err == WSAECONNREFUSED) {
			switch addr := opErr.Addr.(type) {
			case *net.TCPAddr:
				return addr.IP
			case *net.UDPAddr:
				return addr.IP
			}
		}
	}

	return nil
}
