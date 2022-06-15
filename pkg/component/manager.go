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
package component

import (
	"container/list"
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/k0sproject/k0s/pkg/apis/k0s.k0sproject.io/v1beta1"
	"github.com/k0sproject/k0s/pkg/performance"
	"github.com/sirupsen/logrus"
	"go.uber.org/multierr"
	"golang.org/x/sync/errgroup"
)

// Manager manages components
type Manager struct {
	Components     []Component
	HealthyTimeout time.Duration

	started              *list.List
	lastReconciledConfig *v1beta1.ClusterConfig
}

// NewManager creates a manager
func NewManager() *Manager {
	return &Manager{
		Components:     []Component{},
		HealthyTimeout: 2 * time.Minute,
		started:        list.New(),
	}
}

// Add adds a component to the manager
func (m *Manager) Add(ctx context.Context, component Component) {
	m.Components = append(m.Components, component)

	if reconcilable, ok := component.(ReconcilerComponent); ok && m.lastReconciledConfig != nil {
		if err := reconcilable.Reconcile(ctx, m.lastReconciledConfig); err != nil {
			logrus.WithError(err).Warnf("Failed to reconcile %s during addition", reflect.TypeOf(component))
		}
	}
}

// Init initializes all managed components
func (m *Manager) Init(ctx context.Context) error {
	g, _ := errgroup.WithContext(ctx)

	for _, comp := range m.Components {
		compName := reflect.TypeOf(comp).Elem().Name()
		logrus.Infof("initializing %v", compName)
		c := comp
		// init this async
		g.Go(func() error {
			return c.Init(ctx)
		})
	}
	err := g.Wait()
	return err
}

// Start starts all managed components
func (m *Manager) Start(ctx context.Context) error {
	perfTimer := performance.NewTimer("component-start").Buffer().Start()
	for _, comp := range m.Components {
		compName := reflect.TypeOf(comp).Elem().Name()
		perfTimer.Checkpoint(fmt.Sprintf("running-%s", compName))
		logrus.Infof("starting %v", compName)
		if err := comp.Run(ctx); err != nil {
			_ = m.Stop()
			return err
		}
		m.started.PushFront(comp)
		perfTimer.Checkpoint(fmt.Sprintf("running-%s-done", compName))
		if err := waitForHealthy(ctx, comp, compName, m.HealthyTimeout); err != nil {
			_ = m.Stop()
			return err
		}
	}
	perfTimer.Output()
	return nil
}

// Stop stops all managed components
func (m *Manager) Stop() error {
	var ret error
	var next *list.Element

	for e := m.started.Front(); e != nil; e = next {
		component := e.Value.(Component)
		name := reflect.TypeOf(component).Elem().Name()

		if err := component.Stop(); err != nil {
			logrus.Errorf("failed to stop component %s: %s", name, err.Error())
			if ret == nil {
				ret = fmt.Errorf("failed to stop components")
			}
		} else {
			logrus.Infof("stopped component %s", name)
		}

		next = e.Next()
		m.started.Remove(e)
	}
	return ret
}

// Reconcile reconciles all managed components
func (m *Manager) Reconcile(ctx context.Context, cfg *v1beta1.ClusterConfig) error {
	err := Reconcile(ctx, m, cfg)
	m.lastReconciledConfig = cfg
	return err
}

func Reconcile[T any](ctx context.Context, m *Manager, state T) error {
	var errors []error

	logrus.Infof("Starting to reconcile %d components", len(m.Components))
	for _, component := range m.Components {
		compType := reflect.TypeOf(component)
		reconcilable, ok := component.(Reconcilable[T])
		if !ok {
			logrus.Debug(compType, "cannot be reconciled over", reflect.TypeOf(state))
			continue
		}

		logrus.Info("Reconciling", compType)
		if err := reconcilable.Reconcile(ctx, state); err != nil {
			logrus.WithError(err).Debug("Failed to reconcile", compType)
			return err
		}
	}

	logrus.Infof("Done reconciling %d components", len(m.Components))
	return multierr.Combine(errors...)
}

// waitForHealthy waits until the component is healthy and returns true upon success. If a timeout occurs, it returns false
func waitForHealthy(ctx context.Context, comp Component, name string, timeout time.Duration) error {
	ctx, cancelFunction := context.WithTimeout(ctx, timeout)

	// clear up context after timeout
	defer cancelFunction()

	// loop forever, until the context is canceled or until etcd is healthy
	ticker := time.NewTicker(100 * time.Millisecond)
	for {
		logrus.Debugf("checking %s for health", name)
		if err := comp.Healthy(); err != nil {
			logrus.Debugf("health-check: %s not yet healthy: %v", name, err)
		} else {
			return nil
		}

		select {
		case <-ticker.C:
			continue
		case <-ctx.Done():
			return fmt.Errorf("%s health-check timed out", name)
		}
	}
}
