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

package applier

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sync/atomic"
	"time"

	"github.com/k0sproject/k0s/internal/pkg/dir"
	"github.com/k0sproject/k0s/pkg/component/controller/leaderelector"
	"github.com/k0sproject/k0s/pkg/component/manager"
	"github.com/k0sproject/k0s/pkg/config"
	"github.com/k0sproject/k0s/pkg/constant"
	kubeutil "github.com/k0sproject/k0s/pkg/kubernetes"

	"github.com/fsnotify/fsnotify"
	"github.com/sirupsen/logrus"
)

// Manager is the Component interface wrapper for Applier
type Manager struct {
	K0sVars           *config.CfgVars
	KubeClientFactory kubeutil.ClientFactoryInterface

	bundlePath string
	cancel     atomic.Pointer[context.CancelCauseFunc]
	log        logrus.FieldLogger

	LeaderElector leaderelector.Interface
}

var _ manager.Component = (*Manager)(nil)

type stack = struct {
	context.CancelCauseFunc
	*StackApplier
}

// Init initializes the Manager
func (m *Manager) Init(ctx context.Context) error {
	err := dir.Init(m.K0sVars.ManifestsDir, constant.ManifestsDirMode)
	if err != nil {
		return fmt.Errorf("failed to create manifest bundle dir %s: %w", m.K0sVars.ManifestsDir, err)
	}
	if m.log == nil {
		m.log = logrus.StandardLogger()
	}
	m.bundlePath = m.K0sVars.ManifestsDir
	m.log = m.log.WithFields(logrus.Fields{
		"component": "applier",
		"path":      m.bundlePath,
	})

	m.LeaderElector.AddAcquiredLeaseCallback(func() {
		ctx, cancel := context.WithCancelCause(context.Background())
		for {
			prevCancel := m.cancel.Load()
			if prevCancel == &managerStopped {
				return
			}

			if m.cancel.CompareAndSwap(prevCancel, &cancel) {
				if prevCancel != nil {
					(*prevCancel)(errors.New("replaced after having acquired a new lease"))
				}
				break
			}
		}

		go func() {
			// FIXME retry on err???
			_ = m.runStacks(ctx)
		}()
	})
	m.LeaderElector.AddLostLeaseCallback(func() {
		if cancel := m.cancel.Load(); cancel != nil {
			(*cancel)(errors.New("lost lease"))
		}
	})

	return err
}

// Run runs the Manager
func (m *Manager) Start(_ context.Context) error {
	return nil
}

var managerStopped = context.CancelCauseFunc(func(error) {})

// Stop stops the Manager
func (m *Manager) Stop() error {
	if cancel := m.cancel.Swap(&managerStopped); cancel != nil {
		(*cancel)(errors.New("manager stopped"))
	}
	return nil
}

func (m *Manager) runStacks(ctx context.Context) error {
	log := m.log.WithField("component", "applier").WithField("path", m.bundlePath)

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.WithError(err).Error("failed to create watcher")
		return err
	}
	defer watcher.Close()

	err = watcher.Add(m.bundlePath)
	if err != nil {
		log.WithError(err).Warn("Failed to start watcher")
	}

	// Add all directories after the bundle dir has been added to the watcher.
	// Doing it the other way round introduces a race condition when directories
	// get created after the initial listing but before the watch starts.

	dirs, err := dir.GetAll(m.bundlePath)
	if err != nil {
		return err
	}

	stacks := make(map[string]stack)

	for _, dir := range dirs {
		m.createStack(ctx, stacks, path.Join(m.bundlePath, dir))
	}

	for {
		select {
		case err, ok := <-watcher.Errors:
			if !ok {
				return err
			}

			log.WithError(err).Warn("Error while watching manifest directory")

		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			switch event.Op {
			case fsnotify.Create:
				if dir.IsDirectory(event.Name) {
					m.createStack(ctx, stacks, event.Name)
				}
			case fsnotify.Remove:
				m.removeStack(ctx, stacks, event.Name)
			}
		case <-ctx.Done():
			log.WithField("reason", context.Cause(ctx)).Info("Manifest watcher done")
			return nil
		}
	}
}

func (m *Manager) createStack(ctx context.Context, stacks map[string]stack, path string) {
	ctx, cancel := context.WithCancelCause(ctx)
	stack := stack{cancel, NewStackApplier(path, m.KubeClientFactory, m.log)}

	if prevStack, ok := stacks[path]; ok {
		prevStack.CancelCauseFunc(errors.New("replaced by new stack"))
	}
	stacks[path] = stack

	go func() {
		log := m.log.WithField("stack", path)
		log.Info("Running stack")
		for {
			if err := stack.Run(ctx); err != nil {
				log.WithError(err).Error("Failed to run stack")
			}

			select {
			case <-time.After(10 * time.Second):
				log.Info("Restarting stack")
				continue
			case <-ctx.Done():
				log.WithField("reason", context.Cause(ctx)).Info("Stack done")
				return
			}
		}
	}()
}

func (m *Manager) removeStack(ctx context.Context, stacks map[string]stack, name string) {
	stack, ok := stacks[name]
	if !ok {
		m.log.
			WithField("path", name).
			Debug("Attempted to remove non-existent stack, probably not a directory")
		return
	}

	stack.CancelCauseFunc(errors.New("directory has been removed from file system"))
	delete(stacks, name)

	log := m.log.WithField("stack", name)
	if err := stack.DeleteStack(ctx); err != nil {
		log.WithError(err).Error("Failed to delete stack")
		return
	}

	log.Info("Stack deleted successfully")
}
