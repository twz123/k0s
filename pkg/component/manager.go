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
	if isReconcileComponent(component) && m.lastReconciledConfig != nil {
		if err := m.reconcileComponent(ctx, component, m.lastReconciledConfig); err != nil {
			logrus.WithError(err).Warn("component reconciler failed")
		}
	}
}

// Init initializes all managed components
func (m *Manager) Init(ctx context.Context) error {
	g, _ := errgroup.WithContext(ctx)

	for _, comp := range m.Components {
		logrus.Info("initializing ", componentName(comp))
		c := comp
		// init this async
		g.Go(func() error {
			if err := c.Init(ctx); err != nil {
				return &Error{c, err}
			}
			return nil
		})
	}
	err := g.Wait()
	return err
}

// Start starts all managed components
func (m *Manager) Start(ctx context.Context) error {
	perfTimer := performance.NewTimer("component-start").Buffer().Start()
	for _, comp := range m.Components {
		compName := componentName(comp)
		perfTimer.Checkpoint(fmt.Sprintf("running-%s", compName))
		logrus.Info("starting ", compName)
		if err := comp.Run(ctx); err != nil {
			_ = m.Stop()
			return &Error{comp, fmt.Errorf("failed to run: %w", err)}
		}
		m.started.PushFront(comp)
		perfTimer.Checkpoint(fmt.Sprintf("running-%s-done", compName))
		if err := waitForHealthy(ctx, comp, compName, m.HealthyTimeout); err != nil {
			_ = m.Stop()
			return &Error{comp, fmt.Errorf("unhealthy: %w", err)}
		}
	}
	perfTimer.Output()
	return nil
}

// Stop stops all managed components
func (m *Manager) Stop() error {
	var errs []error
	var next *list.Element

	for e := m.started.Front(); e != nil; e = next {
		component := e.Value.(Component)

		if err := component.Stop(); err != nil {
			errs = append(errs, &Error{component, err})
		} else {
			logrus.Info("stopped component ", componentName(component))
		}

		next = e.Next()
		m.started.Remove(e)
	}

	return multierr.Combine(errs...)
}

// Reconcile reconciles all managed components
func (m *Manager) Reconcile(ctx context.Context, cfg *v1beta1.ClusterConfig) error {
	var errs []error
	logrus.Infof("starting component reconciling for %d components", len(m.Components))
	for _, component := range m.Components {
		if err := m.reconcileComponent(ctx, component, cfg); err != nil {
			errs = append(errs, &Error{component, err})
		}
	}
	m.lastReconciledConfig = cfg

	if len(errs) > 0 {
		logrus.Debug("all components reconciled")
	}

	return multierr.Combine(errs...)
}

func (m *Manager) reconcileComponent(ctx context.Context, component Component, cfg *v1beta1.ClusterConfig) error {
	clusterComponent, ok := component.(ReconcilerComponent)
	compName := componentName(component)
	if !ok {
		logrus.Debugf("%s does not implement the ReconcileComponent interface --> not reconciling it", compName)
		return nil
	}
	logrus.Infof("starting to reconcile %s", compName)
	if err := clusterComponent.Reconcile(ctx, cfg); err != nil {
		logrus.WithError(err).Errorf("failed to reconcile component %s", compName)
		return err
	}
	return nil
}

func isReconcileComponent(component Component) bool {
	_, ok := component.(ReconcilerComponent)
	return ok
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
			logrus.WithError(err).Debugf("health-check: %s not yet healthy", name)
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

func componentName(component Component) string {
	return reflect.TypeOf(component).Elem().Name()
}

type Error struct {
	Component Component
	Err       error
}

func (e *Error) Error() string {
	var name any
	if e.Component != nil {
		name = componentName(e.Component)
	}

	return fmt.Sprintf("%v: %s", name, e.Err.Error())
}

func (e *Error) Unwrap() error {
	return e.Err
}
