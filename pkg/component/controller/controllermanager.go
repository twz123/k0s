// SPDX-FileCopyrightText: 2020 k0s authors
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/sirupsen/logrus"

	"github.com/k0sproject/k0s/internal/pkg/file"
	"github.com/k0sproject/k0s/internal/pkg/flags"
	"github.com/k0sproject/k0s/internal/pkg/stringmap"
	"github.com/k0sproject/k0s/internal/pkg/users"
	"github.com/k0sproject/k0s/pkg/apis/k0s/v1beta1"
	"github.com/k0sproject/k0s/pkg/assets"
	"github.com/k0sproject/k0s/pkg/component/featuregates"
	"github.com/k0sproject/k0s/pkg/component/manager"
	"github.com/k0sproject/k0s/pkg/config"
	"github.com/k0sproject/k0s/pkg/constant"
	"github.com/k0sproject/k0s/pkg/supervisor"
)

// Manager implement the component interface to run kube scheduler
type Manager struct {
	K0sVars               *config.CfgVars
	LogLevel              string
	DisableLeaderElection bool
	ServiceClusterIPRange string
	PrimaryAddressFamily  v1beta1.PrimaryAddressFamilyType
	ExtraArgs             string
	// The CA for the kubernetes.io/kubelet-serving signer. If nil, kubelet
	// serving certificates are issued by the cluster CA, like the
	// certificates of all the other signers.
	KubeletServingCA *KubeletServingCA

	supervisor     *supervisor.Supervisor
	executablePath string
	uid            int
	previousConfig stringmap.StringMap
}

var cmDefaultArgs = stringmap.StringMap{
	"allocate-node-cidrs":             "true",
	"bind-address":                    "127.0.0.1",
	"cluster-name":                    "k0s",
	"controllers":                     "*,bootstrapsigner,tokencleaner",
	"leader-elect":                    "true",
	"use-service-account-credentials": "true",
}

const kubeControllerManagerComponent = "kube-controller-manager"

var _ manager.Component = (*Manager)(nil)
var _ manager.Reconciler = (*Manager)(nil)

// Init extracts the needed binaries
func (a *Manager) Init(_ context.Context) error {
	var err error
	// controller manager running as api-server user as they both need access to same sa.key
	a.uid, err = users.LookupUID(constant.ApiserverUser)
	if err != nil {
		err = fmt.Errorf("failed to lookup UID for %q: %w", constant.ApiserverUser, err)
		a.uid = users.RootUID
		logrus.WithError(err).Warn("Running Kubernetes controller manager as root")
	}

	// controller manager should be the only component that needs access to
	// ca.key so let it own it.
	if err := os.Chown(filepath.Join(a.K0sVars.CertRootDir, "ca.key"), a.uid, -1); err != nil && os.Geteuid() == 0 {
		logrus.Warn("failed to change permissions for the ca.key: ", err)
	}
	if err := a.writeKubeletServingCA(); err != nil {
		return err
	}
	a.executablePath, err = assets.StageExecutable(a.K0sVars.BinDir, kubeControllerManagerComponent)
	return err
}

// Writes the kubelet-serving CA's key and certificate to the run directory,
// for the kubernetes.io/kubelet-serving signer. Does nothing if there's no
// kubelet-serving CA. The key is owned by the controller manager's user, like
// the cluster CA key.
func (a *Manager) writeKubeletServingCA() error {
	if a.KubeletServingCA == nil {
		return nil
	}

	keyPEM, err := a.KubeletServingCA.KeyPEM()
	if err != nil {
		return fmt.Errorf("failed to encode kubelet-serving CA key: %w", err)
	}
	keyFile := filepath.Join(a.K0sVars.RunDir, kubeletServingCAKeyFile)
	if err := file.WriteContentAtomically(keyFile, keyPEM, constant.CertSecureMode); err != nil {
		return fmt.Errorf("failed to write kubelet-serving CA key: %w", err)
	}
	if err := file.Chown(keyFile, a.uid, constant.CertSecureMode); err != nil {
		return fmt.Errorf("failed to change ownership of kubelet-serving CA key: %w", err)
	}

	certFile := filepath.Join(a.K0sVars.RunDir, kubeletServingCACertFile)
	if err := file.WriteContentAtomically(certFile, a.KubeletServingCA.CertPEM(), constant.CertMode); err != nil {
		return fmt.Errorf("failed to write kubelet-serving CA certificate: %w", err)
	}

	return nil
}

// Run runs kube Manager
func (a *Manager) Start(_ context.Context) error { return nil }

// Reconcile detects changes in configuration and applies them to the component
func (a *Manager) Reconcile(ctx context.Context, clusterConfig *v1beta1.ClusterConfig) error {
	logger := logrus.WithField("component", kubeControllerManagerComponent)
	logger.Info("Starting reconcile")

	args := a.buildArgs(logger, clusterConfig)

	if args.Equals(a.previousConfig) && a.supervisor != nil {
		// no changes and supervisor already running, do nothing
		logger.Info("reconcile has nothing to do")
		return nil
	}
	// Stop in case there's process running already and we need to change the config
	if a.supervisor != nil {
		logger.Info("reconcile has nothing to do")
		if err := a.supervisor.Stop(); err != nil {
			logger.WithError(err).Error("Failed to stop executable")
		}
		a.supervisor = nil
	}

	a.supervisor = &supervisor.Supervisor{
		Name:    kubeControllerManagerComponent,
		BinPath: a.executablePath,
		RunDir:  a.K0sVars.RunDir,
		DataDir: a.K0sVars.DataDir,
		Args:    append(args.ToDashedArgs(), clusterConfig.Spec.ControllerManager.RawArgs...),
		UID:     a.uid,
	}
	a.previousConfig = args
	return a.supervisor.Supervise(ctx)
}

// Assembles the command line arguments for the given cluster configuration.
func (a *Manager) buildArgs(logger logrus.FieldLogger, clusterConfig *v1beta1.ClusterConfig) stringmap.StringMap {
	ccmAuthConf := filepath.Join(a.K0sVars.CertRootDir, "ccm.conf")
	caCert, caKey := filepath.Join(a.K0sVars.CertRootDir, "ca.crt"), filepath.Join(a.K0sVars.CertRootDir, "ca.key")
	args := stringmap.StringMap{
		"authentication-kubeconfig":        ccmAuthConf,
		"authorization-kubeconfig":         ccmAuthConf,
		"kubeconfig":                       ccmAuthConf,
		"client-ca-file":                   caCert,
		"requestheader-client-ca-file":     filepath.Join(a.K0sVars.CertRootDir, "front-proxy-ca.crt"),
		"root-ca-file":                     caCert,
		"service-account-private-key-file": filepath.Join(a.K0sVars.CertRootDir, "sa.key"),
		"cluster-cidr":                     clusterConfig.Spec.Network.BuildPodCIDR(a.PrimaryAddressFamily),
		"service-cluster-ip-range":         a.ServiceClusterIPRange,
		"profiling":                        "false",
		"terminated-pod-gc-threshold":      "12500",
		"v":                                a.LogLevel,
	}

	if a.KubeletServingCA != nil {
		// Kubelet serving certificates are issued by their own CA. Once a
		// signer has files of its own, so must all the others.
		args["cluster-signing-kubelet-serving-cert-file"] = filepath.Join(a.K0sVars.RunDir, kubeletServingCACertFile)
		args["cluster-signing-kubelet-serving-key-file"] = filepath.Join(a.K0sVars.RunDir, kubeletServingCAKeyFile)
		for _, signer := range []string{"kubelet-client", "kube-apiserver-client", "legacy-unknown"} {
			args["cluster-signing-"+signer+"-cert-file"] = caCert
			args["cluster-signing-"+signer+"-key-file"] = caKey
		}
	} else {
		args["cluster-signing-cert-file"] = caCert
		args["cluster-signing-key-file"] = caKey
	}

	// Handle the extra args as last so they can be used to override some k0s "hardcodings"
	if a.ExtraArgs != "" {
		// This service uses args without hyphens, so enforce that.
		extras := flags.Split(strings.ReplaceAll(a.ExtraArgs, "--", ""))
		args.Merge(extras)
	}

	if clusterConfig.Spec.Network.DualStack.Enabled {
		args["node-cidr-mask-size-ipv6"] = "117"
		args["node-cidr-mask-size-ipv4"] = "24"
	} else if clusterConfig.Spec.Network.IsSingleStackIPv6() {
		args["node-cidr-mask-size"] = "117"
	} else {
		args["node-cidr-mask-size"] = "24"
	}
	for name, value := range clusterConfig.Spec.ControllerManager.ExtraArgs {
		if _, ok := args[name]; ok {
			logger.Warnf("overriding kube-controller-manager flag with user provided value: %s", name)
		}
		args[name] = value
	}

	// The controller manager refuses to start when the catch-all signing flags
	// are combined with per-signer ones. Users who provide the former get what
	// they asked for: all certificates signed by that CA, including kubelet
	// serving ones. A missing half of the pair is filled in with the cluster
	// CA, since the controller manager skips signing altogether otherwise.
	if a.KubeletServingCA != nil && hasClusterSigningCatchAll(args, clusterConfig.Spec.ControllerManager.RawArgs) {
		logger.Warn("The cluster signing certificate or key has been overridden, kubelet serving certificates will be issued by that CA instead of the kubelet-serving CA")
		for _, signer := range []string{"kubelet-serving", "kubelet-client", "kube-apiserver-client", "legacy-unknown"} {
			delete(args, "cluster-signing-"+signer+"-cert-file")
			delete(args, "cluster-signing-"+signer+"-key-file")
		}
		if _, ok := args["cluster-signing-cert-file"]; !ok {
			args["cluster-signing-cert-file"] = caCert
		}
		if _, ok := args["cluster-signing-key-file"]; !ok {
			args["cluster-signing-key-file"] = caKey
		}
	}

	for name, value := range cmDefaultArgs {
		if args[name] == "" {
			args[name] = value
		}
	}
	if a.DisableLeaderElection {
		args["leader-elect"] = "false"
	}

	return featuregates.ToArgs(args, clusterConfig.Spec.FeatureGates, kubeControllerManagerComponent)
}

// Indicates whether the catch-all cluster signing flags are among the given
// arguments.
func hasClusterSigningCatchAll(args stringmap.StringMap, rawArgs []string) bool {
	for _, name := range []string{"cluster-signing-cert-file", "cluster-signing-key-file"} {
		if _, ok := args[name]; ok {
			return true
		}
		if slices.ContainsFunc(rawArgs, func(rawArg string) bool {
			return strings.HasPrefix(rawArg, "--"+name)
		}) {
			return true
		}
	}
	return false
}

// Stop stops Manager
func (a *Manager) Stop() error {
	if a.supervisor != nil {
		return a.supervisor.Stop()
	}
	return nil
}
