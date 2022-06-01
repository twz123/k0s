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
package applier

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sync"
	"sync/atomic"
	"time"

	"github.com/k0sproject/k0s/internal/pkg/dir"
	"github.com/k0sproject/k0s/pkg/component/controller"
	"github.com/k0sproject/k0s/pkg/constant"
	"github.com/k0sproject/k0s/pkg/kubernetes"

	"github.com/fsnotify/fsnotify"
	"github.com/sirupsen/logrus"
)

// Manager is the Component interface wrapper for Applier
type Manager struct {
	K0sVars           constant.CfgVars
	KubeClientFactory kubernetes.ClientFactoryInterface

	bundleDir     string
	cancelWatcher atomic.Value
	log           *logrus.Entry

	leaderMu      sync.Mutex
	LeaderElector controller.LeaderElector
	stopped       bool
}

type stack = struct {
	context.CancelFunc
	*StackApplier
}

type stacks = map[string]stack

// Init initializes the Manager
func (m *Manager) Init(ctx context.Context) error {
	m.log = logrus.WithField("component", "applier-manager")

	m.bundleDir = m.K0sVars.ManifestsDir
	err := dir.Init(m.bundleDir, constant.ManifestsDirMode)
	if err != nil {
		return fmt.Errorf("failed to create bundle dir %s: %w", m.bundleDir, err)
	}

	return err
}

// Run runs the Manager
func (m *Manager) Run(context.Context) error {
	m.LeaderElector.AddAcquiredLeaseCallback(func() {
		m.leaderMu.Lock()
		defer m.leaderMu.Unlock()

		if !m.stopped {
			watcherCtx, cancel := context.WithCancel(context.Background())
			if m.swapCancel(cancel) {
				m.log.Info("Cancelled stacks")
			}
			m.log.Info("Acquired lease, running stacks")
			go m.loopStacks(watcherCtx)
		}
	})

	m.LeaderElector.AddLostLeaseCallback(func() {
		m.leaderMu.Lock()
		defer m.leaderMu.Unlock()

		if m.swapCancel(nil) {
			m.log.Info("Lost lease, stopped stacks")
		}
	})

	return nil
}

// Health-check interface
func (m *Manager) Healthy() error { return nil }

// Stop stops the Manager
func (m *Manager) Stop() error {
	m.leaderMu.Lock()
	m.stopped = true
	swapped := m.swapCancel(nil)
	m.leaderMu.Unlock()

	if swapped {
		m.log.Info("Stopped")
	}

	return nil
}

func (m *Manager) swapCancel(new context.CancelFunc) bool {
	if old, ok := m.cancelWatcher.Swap(&new).(*context.CancelFunc); ok && *old != nil {
		(*old)()
		return true
	}

	return false
}

func (m *Manager) loopStacks(ctx context.Context) {
	for ctx.Err() == nil {
		if err := m.runStacks(ctx); err != nil {
			m.log.WithError(err).Error("Failed to run stacks")
		}

		timer := time.NewTimer(10 * time.Second)
		select {
		case <-timer.C:
			m.log.Info("Restarting")
			continue
		case <-ctx.Done():
			timer.Stop()
			break
		}

	}

	m.log.Info("Stacks done")
}

func (m *Manager) runStacks(ctx context.Context) error {
	stacks := make(stacks)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create watcher: %w", err)
	}
	defer watcher.Close()

	err = watcher.Add(m.bundleDir)
	if err != nil {
		return fmt.Errorf("failed to watch %q: %w", m.bundleDir, err)
	}

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Add all directories after the bundle dir has been added to the watcher.
	// Doing it the other way round introduces a race condition when directories
	// get created after the initial listing but before the watch starts.
	if dirs, err := dir.GetAll(m.bundleDir); err == nil {
		for _, dir := range dirs {
			if err := m.createStack(runCtx, stacks, path.Join(m.bundleDir, dir)); err != nil {
				return fmt.Errorf("failed to create stack for %q: %w", dir, err)
			}
		}
	} else {
		return fmt.Errorf("failed to list all bundle directories in %q: %w", m.bundleDir, err)
	}

	for {
		select {
		case err, ok := <-watcher.Errors:
			if !ok {
				return errors.New("error channel closed")
			}

			m.log.WithError(err).Warn("Error while watching directory")

		case event, ok := <-watcher.Events:
			if !ok {
				return errors.New("event channel closed")
			}

			switch event.Op {
			case fsnotify.Create:
				if !dir.IsDirectory(event.Name) {
					continue
				}
				if err := m.createStack(runCtx, stacks, event.Name); err != nil {
					return err
				}

			case fsnotify.Remove:
				m.deleteStack(runCtx, stacks, event.Name)

			default:
				m.log.Debug("Ignoring ", event)
			}

		case <-runCtx.Done():
			return nil
		}
	}
}

func (m *Manager) createStack(ctx context.Context, stacks stacks, name string) error {
	// safeguard in case the fswatcher would trigger an event for an already existing watcher
	if _, ok := stacks[name]; ok {
		return nil
	}

	stackCtx, cancelStack := context.WithCancel(ctx)
	stack := stack{cancelStack, NewStackApplier(name, m.KubeClientFactory)}
	stacks[name] = stack

	go func() {
		log := m.log.WithField("stack", name)
		for timer := time.NewTimer(0 * time.Second); ; timer.Reset(10 * time.Second) {
			select {
			case <-timer.C:
				log.Info("Running stack")
				if err := stack.Run(stackCtx); err != nil {
					log.WithError(err).Error("Failed to run stack")
				}

			case <-stackCtx.Done():
				timer.Stop()
				log.Info("Stack done")
				return
			}
		}
	}()

	return nil
}

func (m *Manager) deleteStack(ctx context.Context, stacks stacks, name string) {
	log := m.log.WithField("stack", name)

	stack, ok := stacks[name]
	if !ok {
		m.log.Debug("Attempted to delete non-existent stack, probably not a directory")
		return
	}

	delete(stacks, name)
	stack.CancelFunc()

	if err := stack.DeleteStack(ctx); err != nil {
		log.WithError(err).Error("Failed to delete stack")
	} else {
		log.Info("Stack deleted successfully")
	}
}
