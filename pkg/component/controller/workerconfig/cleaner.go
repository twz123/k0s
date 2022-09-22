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

package workerconfig

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"

	"github.com/k0sproject/k0s/pkg/applier"
	"github.com/k0sproject/k0s/pkg/kubernetes"

	"github.com/sirupsen/logrus"
	"go.uber.org/multierr"
)

type cleaner interface {
	init()
	reconciled(context.Context)
	stop()
}

// kubeletConfigCleaner takes care of removing the bits
// and pieces of the replaced kubelet-config component.
type kubeletConfigCleaner struct {
	log logrus.FieldLogger

	dir           string
	clientFactory kubernetes.ClientFactoryInterface

	doStop atomic.Value
}

func (c *kubeletConfigCleaner) init() {
	var errs []error
	for _, path := range []string{filepath.Join(c.dir, "kubelet-config.yaml"), c.dir} {
		if err := os.Remove(path); err == nil {
			c.log.Debugf("Removed deprecated path: %q", path)
		} else if os.IsNotExist(err) {
			c.log.Debugf("Deprecated path doesn't exist: %q", path)
		} else {
			errs = append(errs, err)
		}
	}

	if err := multierr.Combine(errs...); err != nil {
		c.log.WithError(multierr.Combine(errs...)).Warn("Failed to remove some deprecated paths")
	}
}

func (c *kubeletConfigCleaner) reconciled(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})

	// Execute this only once ...
	if !c.doStop.CompareAndSwap(nil, func() {
		cancel()
		<-done
	}) {
		return
	}

	go func() {
		defer func() {
			close(done)
			c.stop()
		}()

		if err := c.removeKubeletStack(ctx); err != nil {
			c.log.WithError(err).Warn("Failed to remove deprecated kubelet stack")
		}
	}()
}

func (c *kubeletConfigCleaner) stop() {
	if stop, ok := c.doStop.Swap(func() {}).(func()); ok {
		stop()
		return
	}
}

func (c *kubeletConfigCleaner) removeKubeletStack(ctx context.Context) error {
	dynamicClient, err := c.clientFactory.GetDynamicClient()
	if err != nil {
		return err
	}
	discoveryClient, err := c.clientFactory.GetDiscoveryClient()
	if err != nil {
		return err
	}

	stack := applier.Stack{
		Name:      "kubelet",
		Client:    dynamicClient,
		Discovery: discoveryClient,
	}

	return stack.Apply(ctx, true)
}
