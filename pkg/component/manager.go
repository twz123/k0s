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
	"context"
	"fmt"
	"math"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/k0sproject/k0s/pkg/apis/k0s.k0sproject.io/v1beta1"
	"github.com/k0sproject/k0s/pkg/performance"
	"go.uber.org/multierr"

	"github.com/avast/retry-go"
	"github.com/sirupsen/logrus"
)

// ManagerBuilder collects components to be managed by a Manager.
type ManagerBuilder struct {
	components []Component
}

// Add adds a to-be-managed component to the builder.
func (b *ManagerBuilder) Add(component Component) {
	b.components = append(b.components, component)
}

// Build creates a manager with all its added components.
func (b *ManagerBuilder) Build(name string) ReconcilerComponent {
	components := b.components
	b.components = nil

	return &Manager{
		log: logrus.WithField("component", name),

		name:       name,
		components: components,
	}
}

// Manager manages components
type Manager struct {
	log logrus.FieldLogger

	name       string
	components []Component

	mu sync.Mutex
}

func (m *Manager) Name() string {
	return m.name
}

// Add adds component to the Manager.
//
// Deprecated: Restructure code so this becomes unnecessary.
func (m *Manager) NotSoGoodAdd(component Component) {
	m.mu.Lock()
	m.components = append(m.components, component)
	m.mu.Unlock()
}

// Init initializes all managed components.
func (m *Manager) Init(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	err := m.forEachComponentAsync(ctx, true, func(ctx context.Context, component Component) error {
		m.log.Info("Initializing component ", componentName(component))
		return component.Init(ctx)
	})

	if err != nil {
		_ = m.forEachComponentAsync(context.Background(), false, func(_ context.Context, component Component) error {
			if err := component.Stop(); err != nil {
				m.log.WithError(err).Debugf("Failed to stop component %s after initialization failure", componentName(component))
			}
			return nil
		})
	}

	return err
}

// Run starts all managed components.
func (m *Manager) Run(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	perfTimer := performance.NewTimer(fmt.Sprintf("component-%s-start", m.name)).Buffer().Start()

	for _, component := range m.components {
		if err := m.startComponent(ctx, component, perfTimer); err != nil {
			if stopErr := m.stop(); stopErr != nil {
				m.log.WithError(stopErr).Debug("Failed to stop components after startup failure")
			}
			return &Error{component, err}
		}
	}

	perfTimer.Output()
	return nil
}

func (m *Manager) startComponent(ctx context.Context, component Component, perfTimer *performance.Timer) error {
	name := componentName(component)
	perfTimer.Checkpoint(fmt.Sprintf("running-%s", name))
	m.log.Info("Starting component ", name)
	if err := component.Run(ctx); err != nil {
		return fmt.Errorf("failed to run: %w", err)
	}
	perfTimer.Checkpoint(fmt.Sprintf("running-%s-done", name))

	timeout, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if err := m.waitForHealthy(timeout, component, name); err != nil {
		return fmt.Errorf("unhealthy: %w", err)
	}

	return nil
}

// waitForHealthy waits until the component is healthy. In case the context gets
// cancelled or times out, the last encountered error of the health check is returned.
func (m *Manager) waitForHealthy(ctx context.Context, component Component, name string) error {
	var lastErr error
	retryErr := retry.Do(
		func() error {
			m.log.Debug("Checking component health for ", name)
			lastErr = component.Healthy()
			if lastErr != nil {
				m.log.WithError(lastErr).Debugf("Component %s not yet healthy", name)
			}
			return lastErr
		},
		retry.Context(ctx),
		retry.Attempts(math.MaxUint),
		retry.MaxDelay(time.Second*10),
		retry.LastErrorOnly(true),
	)

	// Prefer lastErr over retryErr: In case the context was cancelled or timed
	// out, retryErr will just be the err of the context, but not the last
	// encountered err of the health check.
	if retryErr != nil && lastErr != nil {
		return lastErr
	}

	return retryErr
}

func (m *Manager) Healthy() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.forEachComponentAsync(context.TODO(), true, func(_ context.Context, component Component) error {
		return component.Healthy()
	})
}

// Reconcile reconciles all managed components
func (m *Manager) Reconcile(ctx context.Context, cfg *v1beta1.ClusterConfig) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.forEachComponentAsync(ctx, false, func(ctx context.Context, component Component) error {
		return m.reconcileComponent(ctx, component, cfg)
	})
}

// Stop stops all managed components
func (m *Manager) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stop()
}

func (m *Manager) stop() error {
	var errs []error

	// Stop components in reverse order
	for i := len(m.components) - 1; i >= 0; i-- {
		component := m.components[i]
		name := reflect.TypeOf(component).Elem().Name()
		m.log.Info("Stopping component ", name)

		if err := component.Stop(); err != nil {
			errs = append(errs, &Error{component, err})
		}
	}

	return multierr.Combine(errs...)
}

// forEachComponentAsync invokes fn concurrently for each component.
// In fail-fast mode, the first encountered error is returned, if any.
// In non fail-fast mode, all errors are returned, if any.
func (m *Manager) forEachComponentAsync(ctx context.Context, failFast bool, fn func(context.Context, Component) error) error {
	// Tracks all concurrent calls to fn.
	var inFlightCalls sync.WaitGroup

	// Create an inner context that can be cancelled for the fail-fast case.
	innerCtx, cancelCtx := context.WithCancel(ctx)

	// This is the channel that will receive any errors.
	errChan := make(chan *Error)

	// This is the combined cancel func for both the inner context and the channel.
	cancel := func() {
		cancelCtx()
		close(errChan)
	}

	// Will be swapped from 0 to 1 if fail-fast and the first error is encountered.
	var errorEncountered int32

	// Call fn concurrently for each component.
	for _, c := range m.components {
		component := c

		inFlightCalls.Add(1)
		go func() {
			defer inFlightCalls.Done()

			// Only call fn if the inner context isn't done yet.
			// This may happen in two cases:
			// 1. The outer context has been cancelled or timed out. In that
			//    case, there might not have been any error yet, so it's
			//    important to pass that on.
			// 2. An error was encountered in fail-fast mode. The error will be
			//    dropped anyways later on, so it doesn't harm to pass it on.
			err := innerCtx.Err()
			if err == nil {
				err = fn(innerCtx, component)
			}
			if err == nil {
				return
			}

			if failFast {
				if atomic.CompareAndSwapInt32(&errorEncountered, 0, 1) {
					// This is the first error that has been encountered. Cancel
					// other concurrent calls to fn and close the channel after
					// this goroutine exits.
					defer cancel()
				} else {
					// An error has already occurred and the channel may have
					// already been closed concurrently.
					return
				}
			}

			errChan <- &Error{component, err}
		}()
	}

	// Ensure that the inner context gets cancelled and the channel gets closed
	// after all concurrent calls to fn, so that this method call returns.
	go func() {
		inFlightCalls.Wait()
		if atomic.LoadInt32(&errorEncountered) == 0 {
			cancel()
		}
	}()

	// Collect all the errs and return them.
	var errs []error
	for err := range errChan {
		errs = append(errs, err)
	}

	return multierr.Combine(errs...)
}

func (m *Manager) reconcileComponent(ctx context.Context, component Component, cfg *v1beta1.ClusterConfig) error {
	if reconcilerComponent, ok := component.(ReconcilerComponent); ok {
		m.log.Info("Reconciling component ", componentName(component))
		return reconcilerComponent.Reconcile(ctx, cfg)
	}

	return nil
}

func componentName(component Component) string {
	if namedComponent, ok := component.(interface{ Name() string }); ok {
		return namedComponent.Name()
	}

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
