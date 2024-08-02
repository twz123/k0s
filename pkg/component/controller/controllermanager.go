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
	"fmt"
	"net"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/k0sproject/k0s/internal/pkg/flags"
	"github.com/k0sproject/k0s/internal/pkg/stringmap"
	"github.com/k0sproject/k0s/internal/pkg/users"
	"github.com/k0sproject/k0s/pkg/apis/k0s/v1beta1"
	"github.com/k0sproject/k0s/pkg/assets"
	"github.com/k0sproject/k0s/pkg/certificate"
	"github.com/k0sproject/k0s/pkg/component/manager"
	"github.com/k0sproject/k0s/pkg/config"
	"github.com/k0sproject/k0s/pkg/constant"
	"github.com/k0sproject/k0s/pkg/supervisor"

	"golang.org/x/sync/errgroup"

	"github.com/sirupsen/logrus"
)

// Manager implement the component interface to run kube scheduler
type Manager struct {
	K0sVars               *config.CfgVars
	LogLevel              string
	SingleNode            bool
	BindAddress           net.IP
	ServiceClusterIPRange string
	ExtraArgs             string

	supervisor     *supervisor.Supervisor
	uid, gid       int
	certificate    *certificate.Certificate
	previousConfig stringmap.StringMap
}

const kubeControllerManagerComponent = "kube-controller-manager"

var _ manager.Component = (*Manager)(nil)
var _ manager.Reconciler = (*Manager)(nil)

// Init extracts the needed binaries
func (a *Manager) Init(ctx context.Context) error {
	log := logrus.WithField("component", kubeControllerManagerComponent)

	eg, ctx := errgroup.WithContext(ctx)
	eg.Go(func() error {
		return assets.Stage(a.K0sVars.BinDir, kubeControllerManagerComponent, constant.BinDirMode)
	})

	eg.Go(func() (err error) {
		// controller manager running as api-server user as they both need access to same sa.key
		a.uid, err = users.GetUID(constant.ApiserverUser)
		if err != nil {
			a.uid = 0
			log.WithError(err).Warn("Running kube-controller-manager as root")
		}
		return nil
	})

	eg.Go(func() (err error) {
		req := certificate.Request{
			Name:      kubeControllerManagerComponent,
			CN:        kubeControllerManagerComponent,
			O:         "kubernetes",
			Hostnames: []string{a.BindAddress.String()},
		}

		a.certificate, err = a.K0sVars.CertManager().EnsureCertificate(req, constant.ApiserverUser)
		return err
	})

	if err := eg.Wait(); err != nil {
		return err
	}

	// controller manager should be the only component that needs access to
	// ca.key so let it own it.
	if err := os.Chown(filepath.Join(a.K0sVars.CertRootDir, "ca.key"), a.uid, -1); err != nil && os.Geteuid() == 0 {
		log.WithError(err).Warn("Failed to change permissions for the CA key file")
	}

	return nil
}

// Run runs kube Manager
func (a *Manager) Start(_ context.Context) error { return nil }

// Reconcile detects changes in configuration and applies them to the component
func (a *Manager) Reconcile(_ context.Context, clusterConfig *v1beta1.ClusterConfig) error {
	logger := logrus.WithField("component", kubeControllerManagerComponent)
	logger.Info("Starting reconcile")
	ccmAuthConf := filepath.Join(a.K0sVars.CertRootDir, "ccm.conf")
	args := stringmap.StringMap{
		"authentication-kubeconfig":        ccmAuthConf,
		"authorization-kubeconfig":         ccmAuthConf,
		"kubeconfig":                       ccmAuthConf,
		"bind-address":                     a.BindAddress.String(),
		"tls-cert-file":                    a.certificate.CertPath,
		"tls-private-key-file":             a.certificate.KeyPath,
		"tls-min-version":                  "VersionTLS12",
		"tls-cipher-suites":                constant.AllowedTLS12CipherSuiteNames(),
		"client-ca-file":                   path.Join(a.K0sVars.CertRootDir, "ca.crt"),
		"cluster-signing-cert-file":        path.Join(a.K0sVars.CertRootDir, "ca.crt"),
		"cluster-signing-key-file":         path.Join(a.K0sVars.CertRootDir, "ca.key"),
		"requestheader-client-ca-file":     path.Join(a.K0sVars.CertRootDir, "front-proxy-ca.crt"),
		"root-ca-file":                     path.Join(a.K0sVars.CertRootDir, "ca.crt"),
		"cluster-name":                     "k0s",
		"controllers":                      "*,bootstrapsigner,tokencleaner",
		"leader-elect":                     fmt.Sprint(!a.SingleNode),
		"use-service-account-credentials":  "true",
		"service-account-private-key-file": path.Join(a.K0sVars.CertRootDir, "sa.key"),
		"allocate-node-cidrs":              "true",
		"cluster-cidr":                     clusterConfig.Spec.Network.BuildPodCIDR(),
		"service-cluster-ip-range":         a.ServiceClusterIPRange,
		"profiling":                        "false",
		"terminated-pod-gc-threshold":      "12500",
		"v":                                a.LogLevel,
	}

	// Handle the extra args as last so they can be used to overrride some k0s "hardcodings"
	if a.ExtraArgs != "" {
		// This service uses args without hyphens, so enforce that.
		extras := flags.Split(strings.ReplaceAll(a.ExtraArgs, "--", ""))
		args.Merge(extras)
	}

	if clusterConfig.Spec.Network.DualStack.Enabled {
		args["node-cidr-mask-size-ipv6"] = "110"
		args["node-cidr-mask-size-ipv4"] = "24"
	} else {
		args["node-cidr-mask-size"] = "24"
	}
	for name, value := range clusterConfig.Spec.ControllerManager.ExtraArgs {
		if _, ok := args[name]; ok {
			logger.Warnf("overriding kube-controller-manager flag with user provided value: %s", name)
		}
		args[name] = value
	}

	args = clusterConfig.Spec.FeatureGates.BuildArgs(args, kubeControllerManagerComponent)

	if args.Equals(a.previousConfig) && a.supervisor != nil {
		// no changes and supervisor already running, do nothing
		logger.Info("reconcile has nothing to do")
		return nil
	}
	// Stop in case there's process running already and we need to change the config
	if a.supervisor != nil {
		logger.Info("reconcile has nothing to do")
		a.supervisor.Stop()
		a.supervisor = nil
	}

	a.supervisor = &supervisor.Supervisor{
		Name:    kubeControllerManagerComponent,
		BinPath: assets.BinPath(kubeControllerManagerComponent, a.K0sVars.BinDir),
		RunDir:  a.K0sVars.RunDir,
		DataDir: a.K0sVars.DataDir,
		Args:    args.ToDashedArgs(),
		UID:     a.uid,
		GID:     a.gid,
	}
	a.previousConfig = args
	return a.supervisor.Supervise()
}

// Stop stops Manager
func (a *Manager) Stop() error {
	if a.supervisor != nil {
		a.supervisor.Stop()
	}
	return nil
}
