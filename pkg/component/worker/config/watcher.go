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
	"time"

	"github.com/k0sproject/k0s/pkg/component/manager"
	"github.com/k0sproject/k0s/pkg/kubernetes"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/wait"
)

type Watcher struct {
	Clients       kubernetes.ClientFactoryInterface
	ProfileName   string
	CacheDir      string
	UpdateProfile func(*Profile)
}

// Init implements manager.Component.
func (w *Watcher) Init(context.Context) error {
	return nil
}

// Start implements manager.Component.
func (w *Watcher) Start(ctx context.Context) error {
	log := logrus.WithField("component", "workerconfig.Watcher")

	client, err := w.Clients.GetClient()
	if err != nil {
		return err
	}

	wait.UntilWithContext(ctx, func(ctx context.Context) {
		err := WatchProfile(
			ctx, log, client, w.CacheDir, w.ProfileName, func(p Profile) error {
				w.UpdateProfile(&p)
				return nil
			},
		)
		if err != nil && !errors.Is(err, context.Cause(ctx)) {
			log.WithError(err).Error("Failed to watch worker profiles")
		}
	}, 10*time.Second)

	return nil
}

// Stop implements manager.Component.
func (w *Watcher) Stop() error {
	panic("unimplemented")
}

var _ manager.Component = (*Watcher)(nil)
