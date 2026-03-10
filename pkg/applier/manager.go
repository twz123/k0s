// SPDX-FileCopyrightText: 2020 k0s authors
// SPDX-License-Identifier: Apache-2.0

package applier

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"time"

	internalos "github.com/k0sproject/k0s/internal/os"
	"github.com/k0sproject/k0s/internal/pkg/dir"
	"github.com/k0sproject/k0s/internal/pkg/file"
	"github.com/k0sproject/k0s/internal/pkg/log"
	"github.com/k0sproject/k0s/pkg/component/controller/leaderelector"
	"github.com/k0sproject/k0s/pkg/component/manager"
	"github.com/k0sproject/k0s/pkg/config"
	"github.com/k0sproject/k0s/pkg/constant"
	kubeutil "github.com/k0sproject/k0s/pkg/kubernetes"
	"github.com/k0sproject/k0s/pkg/leaderelection"

	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/sirupsen/logrus"
)

// Manager is the Component interface wrapper for Applier
type Manager struct {
	K0sVars           *config.CfgVars
	IgnoredStacks     []string
	KubeClientFactory kubeutil.ClientFactoryInterface

	bundleDir string
	stop      func()
	log       *logrus.Entry

	LeaderElector leaderelector.Interface
}

var _ manager.Component = (*Manager)(nil)

type stack = struct {
	cancel  context.CancelCauseFunc
	stopped <-chan struct{}
	*StackApplier
}

// Init initializes the Manager
func (m *Manager) Init(ctx context.Context) error {
	err := dir.Init(m.K0sVars.ManifestsDir, constant.ManifestsDirMode)
	if err != nil {
		return fmt.Errorf("failed to create manifest bundle dir %s: %w", m.K0sVars.ManifestsDir, err)
	}
	m.log = logrus.WithField("component", constant.ApplierManagerComponentName)
	m.bundleDir = m.K0sVars.ManifestsDir

	return nil
}

// Run runs the Manager
func (m *Manager) Start(context.Context) error {
	ctx, cancel := context.WithCancelCause(context.Background())
	stopped := make(chan struct{})

	m.stop = func() {
		cancel(errors.New("applier manager is stopping"))
		<-stopped
	}

	go func() {
		defer close(stopped)
		leaderelection.RunLeaderTasks(ctx, m.LeaderElector.CurrentStatus, func(ctx context.Context) {
			wait.JitterUntilWithContext(ctx, m.runWatchers, 1*time.Minute, 1.3, true)
		})
	}()

	return nil
}

// Stop stops the Manager
func (m *Manager) Stop() error {
	if m.stop != nil {
		m.stop()
	}
	return nil
}

func (m *Manager) runWatchers(ctx context.Context) {
	stacks := make(map[string]stack)
	stackCtx, cancel := context.WithCancelCause(ctx)

	defer func() {
		cancel(context.Cause(ctx))
		for _, stack := range stacks {
			<-stack.stopped
		}
	}()

	dirWatcher := stacksDirWatcher{
		log:         m.log,
		cancel:      cancel,
		createStack: func(name string) { m.createStack(stackCtx, stacks, name) },
		removeStack: func(name string) { m.removeStack(stackCtx, stacks, name) },
	}

	if err := internalos.WatchDir(log.AttachToContext(stackCtx, m.log), m.bundleDir, &dirWatcher); err != nil {
		cancel(err)
		m.log.WithError(err).Error("Failed to watch manifests directory")
	} else {
		m.log.Infof("Watch loop done (%v)", context.Cause(ctx))
	}
}

type stacksDirWatcher struct {
	log         logrus.FieldLogger
	cancel      context.CancelCauseFunc
	createStack func(name string)
	removeStack func(name string)
}

// Activated implements [internalos.DirWatcher].
func (w *stacksDirWatcher) Activated(path string) {
	entries, err := os.ReadDir(path)
	if err != nil {
		w.cancel(err)
		return
	}

	for _, entry := range entries {
		if entry.IsDir() {
			w.createStack(entry.Name())
		}
	}
}

// Touched implements [internalos.DirWatcher].
func (w *stacksDirWatcher) Touched(name string, info func() (fs.FileInfo, error)) {
	if info, err := info(); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			w.log.WithError(err).Warn("Ignoring ", name)
		}
	} else if info.IsDir() {
		w.createStack(name)
	}
}

// Removed implements [internalos.DirWatcher].
func (w *stacksDirWatcher) Removed(name string) {
	w.removeStack(name)
}

func (m *Manager) createStack(ctx context.Context, stacks map[string]stack, name string) {
	// No-op if the stack has already been created.
	if _, ok := stacks[name]; ok {
		return
	}

	log := m.log.WithField("stack", name)

	if slices.Contains(m.IgnoredStacks, name) {
		if err := file.AtomicWithTarget(filepath.Join(m.bundleDir, name, "ignored.txt")).WriteString(
			"The " + name + " stack is handled internally.\n" +
				"This directory is ignored and can be safely removed.\n",
		); err != nil {
			log.WithError(err).Warn("Failed to write ignore notice")
		}
		return
	}

	ctx, cancel := context.WithCancelCause(ctx)
	stopped := make(chan struct{})

	stack := stack{cancel, stopped, NewStackApplier(filepath.Join(m.bundleDir, name), m.KubeClientFactory)}
	stacks[name] = stack

	go func() {
		defer close(stopped)

		wait.JitterUntilWithContext(ctx, func(ctx context.Context) {
			log.Info("Running stack")
			if err := stack.Run(ctx); err != nil {
				log.WithError(err).Error("Failed to run stack")
			}
		}, 1*time.Minute, 1.3, true)

		log.Infof("Stack done (%v)", context.Cause(ctx))
	}()
}

func (m *Manager) removeStack(ctx context.Context, stacks map[string]stack, name string) {
	stack, ok := stacks[name]
	if !ok {
		m.log.Debug("Attempted to remove non-existent stack, probably not a directory: ", name)
		return
	}

	delete(stacks, name)
	stack.cancel(errors.New("stack removed"))
	<-stack.stopped

	log := m.log.WithField("stack", name)
	if err := stack.DeleteStack(ctx); err != nil {
		log.WithError(err).Error("Failed to delete stack")
		return
	}

	log.Info("Stack deleted successfully")
}
