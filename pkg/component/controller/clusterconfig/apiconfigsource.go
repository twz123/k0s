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

package clusterconfig

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	k0sclient "github.com/k0sproject/k0s/pkg/apis/k0s.k0sproject.io/clientset/typed/k0s.k0sproject.io/v1beta1"
	"github.com/k0sproject/k0s/pkg/component"
	"github.com/k0sproject/k0s/pkg/constant"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/sirupsen/logrus"
)

type apiConfigSource struct {
	log logrus.FieldLogger

	configClient k0sclient.ClusterConfigInterface
	receiver     component.Reconcilable

	wg                    sync.WaitGroup
	cancel                context.CancelFunc
	lastOutcome           atomic.Value
	lastReconciledVersion string
}

// NewAPIConfigSource polls the API server periodically for changes to the global
// cluster config object and, if it changed, reconciles its receiver with it.
func NewAPIConfigSource(configClient k0sclient.ClusterConfigInterface, receiver component.Reconcilable) component.Component {
	return &apiConfigSource{
		log: logrus.WithField("component", "api_config_source"),

		configClient: configClient,
		receiver:     receiver,
	}
}

func (*apiConfigSource) Init(context.Context) error { return nil }

func (a *apiConfigSource) Run(context.Context) (err error) {
	if a.cancel != nil {
		return errors.New("already running")
	}

	ctx, cancel := context.WithCancel(context.Background())
	a.cancel = cancel

	a.wg.Add(1)
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer func() {
			ticker.Stop()
			a.wg.Done()
		}()

		for {
			select {
			case <-ticker.C:
				a.pollAndReconcile(ctx)

			case <-ctx.Done():
				return
			}
		}
	}()

	return nil
}

func (a *apiConfigSource) Healthy() error {
	errPtr := a.lastOutcome.Load().(*error)
	if errPtr == nil {
		return errors.New("not yet reconciled")
	}
	return *errPtr
}

func (a *apiConfigSource) Stop() error {
	cancel := a.cancel
	if cancel != nil {
		cancel()
	}

	a.wg.Wait()
	return nil
}

func (a *apiConfigSource) pollAndReconcile(ctx context.Context) {
	// Poll the cluster config
	timeout, cancelTimeout := context.WithTimeout(ctx, 10*time.Second)
	cfg, err := a.configClient.Get(timeout, constant.ClusterConfigObjectName, metav1.GetOptions{})
	cancelTimeout()
	if err != nil {
		a.log.WithError(err).Errorf(
			"Failed to poll for changes to global cluster config, last reconciled version is %q",
			a.lastReconciledVersion,
		)
		return
	}

	currentVersion := cfg.ResourceVersion

	// Push changes only when the config actually changes
	if a.lastReconciledVersion == currentVersion {
		return
	}

	err = a.receiver.Reconcile(ctx, cfg)
	if err == nil {
		a.log.Debugf(
			"Successfully reconciled global cluster config from version %q to %q",
			a.lastReconciledVersion, currentVersion,
		)

		a.lastReconciledVersion = currentVersion
	}
	a.lastOutcome.Store(&err)
}
