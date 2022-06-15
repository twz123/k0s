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
	"strings"
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
			logrus.Warnf("component reconciler failed: %v", err)
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

	startCtx, cancelStartCtx := context.WithCancel(ctx)

	for _, comp := range m.Components {
		compName := reflect.TypeOf(comp).Elem().Name()
		perfTimer.Checkpoint(fmt.Sprintf("running-%s", compName))
		logrus.Infof("starting %v", compName)

		runCh := make(chan error)
		go func() {
			defer close(runCh)
			runCh <- comp.Run(startCtx)
		}()

		select {
		case err := <-runCh:
			if err != nil {
				cancelStartCtx()
				return multierr.Append(err, m.Stop())
			}

		case <-time.After(m.HealthyTimeout):
			cancelStartCtx()
			logrus.Errorf("Timed out while waiting for %s to start", compName)
			err := <-runCh
			if err == nil {
				err = comp.Stop()
			}
			return multierr.Append(fmt.Errorf("%s didn't start in time", compName), err)
		}

		m.started.PushFront(comp)
		perfTimer.Checkpoint(fmt.Sprintf("running-%s-done", compName))
	}
	perfTimer.Output()

	// Pacify go vet lostcancel check: startCtx is only used to conditionally
	// cancel if some timeouts aren't met. It should otherwise just live as long
	// as its parent.
	_ = cancelStartCtx

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

// ReconcileError is just a wrapper for possible many errors
type ReconcileError struct {
	Errors []error
}

// Error returns the stringified error message
func (r ReconcileError) Error() string {
	messages := make([]string, len(r.Errors))
	for i, e := range r.Errors {
		messages[i] = e.Error()
	}
	return strings.Join(messages, "\n")
}

// Reconcile reconciles all managed components
func (m *Manager) Reconcile(ctx context.Context, cfg *v1beta1.ClusterConfig) error {
	errors := make([]error, 0)
	var ret error
	logrus.Infof("starting component reconciling for %d components", len(m.Components))
	for _, component := range m.Components {
		if err := m.reconcileComponent(ctx, component, cfg); err != nil {
			errors = append(errors, err)
		}
	}
	m.lastReconciledConfig = cfg
	if len(errors) > 0 {
		ret = ReconcileError{
			Errors: errors,
		}
	}
	logrus.Debugf("all component reconciled, result: %v", ret)
	return ret
}

func (m *Manager) reconcileComponent(ctx context.Context, component Component, cfg *v1beta1.ClusterConfig) error {
	clusterComponent, ok := component.(ReconcilerComponent)
	compName := reflect.TypeOf(component).String()
	if !ok {
		logrus.Debugf("%s does not implement the ReconcileComponent interface --> not reconciling it", compName)
		return nil
	}
	logrus.Infof("starting to reconcile %s", compName)
	if err := clusterComponent.Reconcile(ctx, cfg); err != nil {
		logrus.Errorf("failed to reconcile component %s: %s", compName, err.Error())
		return err
	}
	return nil
}

func isReconcileComponent(component Component) bool {
	_, ok := component.(ReconcilerComponent)
	return ok
}
