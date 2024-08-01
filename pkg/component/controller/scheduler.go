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
	"path/filepath"

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

// Scheduler implement the component interface to run kube scheduler
type Scheduler struct {
	gid            int
	K0sVars        *config.CfgVars
	LogLevel       string
	SingleNode     bool
	BindAddress    net.IP
	supervisor     *supervisor.Supervisor
	uid            int
	certificate    *certificate.Certificate
	previousConfig stringmap.StringMap
}

var _ manager.Component = (*Scheduler)(nil)
var _ manager.Reconciler = (*Scheduler)(nil)

const kubeSchedulerComponentName = "kube-scheduler"

// Init extracts the needed binaries
func (a *Scheduler) Init(ctx context.Context) error {
	log := logrus.WithField("component", kubeSchedulerComponentName)

	eg, ctx := errgroup.WithContext(ctx)
	eg.Go(func() error {
		return assets.Stage(a.K0sVars.BinDir, kubeSchedulerComponentName, constant.BinDirMode)
	})

	eg.Go(func() (err error) {
		a.uid, err = users.GetUID(constant.SchedulerUser)
		if err != nil {
			a.uid = 0
			log.WithError(err).Warn("Running kube-scheduler as root")
		}
		return nil
	})

	eg.Go(func() (err error) {

		req := certificate.Request{
			Name:      kubeSchedulerComponentName,
			CN:        kubeSchedulerComponentName,
			O:         "kubernetes",
			Hostnames: []string{a.BindAddress.String()},
		}

		a.certificate, err = a.K0sVars.CertManager().EnsureCertificate(req, constant.ApiserverUser)
		return err
	})

	if err := eg.Wait(); err != nil {
		return err
	}

	return nil
}

// Run runs kube scheduler
func (a *Scheduler) Start(_ context.Context) error {
	return nil
}

// Stop stops Scheduler
func (a *Scheduler) Stop() error {
	if a.supervisor != nil {
		a.supervisor.Stop()
	}
	return nil
}

// Reconcile detects changes in configuration and applies them to the component
func (a *Scheduler) Reconcile(_ context.Context, clusterConfig *v1beta1.ClusterConfig) error {
	logrus.Debug("reconcile method called for: Scheduler")

	logrus.Info("Starting kube-scheduler")
	schedulerAuthConf := filepath.Join(a.K0sVars.CertRootDir, "scheduler.conf")
	args := stringmap.StringMap{
		"authentication-kubeconfig": schedulerAuthConf,
		"authorization-kubeconfig":  schedulerAuthConf,
		"kubeconfig":                schedulerAuthConf,
		"bind-address":              a.BindAddress.String(),
		"tls-cert-file":             a.certificate.CertPath,
		"tls-private-key-file":      a.certificate.KeyPath,
		"tls-min-version":           "VersionTLS12",
		"leader-elect":              fmt.Sprint(!a.SingleNode),
		"profiling":                 "false",
		"v":                         a.LogLevel,
	}
	for name, value := range clusterConfig.Spec.Scheduler.ExtraArgs {
		if _, ok := args[name]; ok {
			logrus.Warnf("overriding kube-scheduler flag with user provided value: %s", name)
		}
		args[name] = value
	}
	args = clusterConfig.Spec.FeatureGates.BuildArgs(args, kubeSchedulerComponentName)

	if args.Equals(a.previousConfig) && a.supervisor != nil {
		// no changes and supervisor already running, do nothing
		logrus.WithField("component", kubeSchedulerComponentName).Info("reconcile has nothing to do")
		return nil
	}
	// Stop in case there's process running already and we need to change the config
	if a.supervisor != nil {
		logrus.WithField("component", kubeSchedulerComponentName).Info("reconcile has nothing to do")
		a.supervisor.Stop()
		a.supervisor = nil
	}

	a.supervisor = &supervisor.Supervisor{
		Name:    kubeSchedulerComponentName,
		BinPath: assets.BinPath(kubeSchedulerComponentName, a.K0sVars.BinDir),
		RunDir:  a.K0sVars.RunDir,
		DataDir: a.K0sVars.DataDir,
		Args:    args.ToDashedArgs(),
		UID:     a.uid,
		GID:     a.gid,
	}
	a.previousConfig = args
	return a.supervisor.Supervise()
}
