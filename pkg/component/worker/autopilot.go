//go:build unix

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

package worker

import (
	"context"
	"fmt"
	"time"

	apcli "github.com/k0sproject/k0s/pkg/autopilot/client"
	apcont "github.com/k0sproject/k0s/pkg/autopilot/controller"
	aproot "github.com/k0sproject/k0s/pkg/autopilot/controller/root"
	"github.com/k0sproject/k0s/pkg/component/manager"
	"github.com/k0sproject/k0s/pkg/config"
	"github.com/k0sproject/k0s/pkg/kubernetes"
	"github.com/sirupsen/logrus"

	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"
)

const (
	defaultPollDuration = 5 * time.Second
	defaultPollTimeout  = 5 * time.Minute
)

var _ manager.Component = (*Autopilot)(nil)

type Autopilot struct {
	K0sVars     *config.CfgVars
	CertManager *CertificateManager
}

func (a *Autopilot) Init(ctx context.Context) error {
	return nil
}

func (a *Autopilot) Start(ctx context.Context) error {
	log := logrus.WithFields(logrus.Fields{"component": "autopilot"})

	// Wait 5 mins till we see kubelet auth config in place
	// wait.PollUntilContextTimeout passes it is own ctx argument as a ctx to the given function
	// Poll until the kubelet config can be loaded successfully, as this is the access to the kube api
	// needed by autopilot.
	var restConfig *rest.Config
	if err := wait.PollUntilContextTimeout(ctx, defaultPollDuration, defaultPollTimeout, true, func(ctx context.Context) (_ bool, err error) {
		log.Debug("Attempting to load autopilot client config")
		if restConfig, err = a.CertManager.GetRestConfig(ctx); err != nil {
			log.WithError(err).Warn("Failed to load autopilot client config, retrying in ", defaultPollDuration)
			return false, nil
		}

		return true, nil
	}); err != nil {
		return fmt.Errorf("unable to create autopilot client: %w", context.Cause(ctx))
	}

	autopilotClientFactory := &apcli.ClientFactory{ClientFactoryInterface: &kubernetes.ClientFactory{
		LoadRESTConfig: func() (*rest.Config, error) { return restConfig, nil },
	}}

	log.Info("Autopilot client factory created, booting up worker root controller")
	autopilotRoot, err := apcont.NewRootWorker(aproot.RootConfig{
		KubeConfig:          a.K0sVars.KubeletAuthConfigPath,
		K0sDataDir:          a.K0sVars.DataDir,
		Mode:                "worker",
		ManagerPort:         8899,
		MetricsBindAddr:     "0",
		HealthProbeBindAddr: "0",
	}, log, autopilotClientFactory)
	if err != nil {
		return fmt.Errorf("failed to create autopilot worker: %w", err)
	}

	go func() {
		if err := autopilotRoot.Run(ctx); err != nil {
			logrus.WithError(err).Error("Error running autopilot")

			// TODO: We now have a service with nothing running.. now what?
		}
	}()

	return nil
}

// Stop stops Autopilot
func (a *Autopilot) Stop() error {
	return nil
}
