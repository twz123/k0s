// SPDX-FileCopyrightText: 2020 k0s authors
// SPDX-License-Identifier: Apache-2.0

package applier

import (
	"context"
	"io/fs"
	"path/filepath"
	"sync"
	"time"

	internalos "github.com/k0sproject/k0s/internal/os"
	internallog "github.com/k0sproject/k0s/internal/pkg/log"
	"github.com/k0sproject/k0s/pkg/kubernetes"

	"github.com/avast/retry-go"
	"github.com/sirupsen/logrus"
)

// StackApplier applies a stack whenever the files on disk change.
type StackApplier struct {
	log  logrus.FieldLogger
	path string

	doApply, doDelete func(context.Context) error
}

// NewStackApplier crates new stack applier to manage a stack
func NewStackApplier(path string, kubeClientFactory kubernetes.ClientFactoryInterface) *StackApplier {
	var mu sync.Mutex
	applier := NewApplier(path, kubeClientFactory)

	return &StackApplier{
		log:  logrus.WithField("component", "applier-"+applier.Name),
		path: path,

		doApply: func(ctx context.Context) error {
			mu.Lock()
			defer mu.Unlock()
			return applier.Apply(ctx)
		},

		doDelete: func(ctx context.Context) error {
			mu.Lock()
			defer mu.Unlock()
			return applier.Delete(ctx)
		},
	}
}

// Run watches the stack for updates and executes the initial apply.
func (s *StackApplier) Run(ctx context.Context) error {
	if ctx.Err() != nil {
		return nil
	}

	trigger := make(chan struct{}, 1)
	watchErr := make(chan error, 1)
	debouncer := stackApplierDebouncer{trigger}

	go func() {
		ctx := internallog.AttachToContext(ctx, s.log)
		watchErr <- internalos.WatchDir(ctx, s.path, false, &debouncer)
	}()

	const debounceDelay = 1 * time.Second

	timer := time.NewTimer(debounceDelay)
	defer timer.Stop()

	for {
		select {
		case <-trigger:
			timer.Reset(debounceDelay)
		case <-timer.C:
			s.apply(ctx)
		case err := <-watchErr:
			return err
		case <-ctx.Done():
			return nil
		}
	}
}

func (s *StackApplier) apply(ctx context.Context) {
	s.log.Info("Applying manifests")

	err := retry.Do(
		func() error { return s.doApply(ctx) },
		retry.OnRetry(func(attempt uint, err error) {
			s.log.WithError(err).Warnf("Failed to apply manifests in attempt #%d, retrying after backoff", attempt+1)
		}),
		retry.Context(ctx),
		retry.LastErrorOnly(true),
	)

	if err != nil {
		s.log.WithError(err).Error("Failed to apply manifests")
	}
}

// DeleteStack deletes the associated stack
func (s *StackApplier) DeleteStack(ctx context.Context) error {
	return s.doDelete(ctx)
}

type stackApplierDebouncer struct {
	trigger chan<- struct{}
}

// Touched implements [internalos.DirWatcher].
func (s *stackApplierDebouncer) Touched(name string, _ func() (fs.FileInfo, error)) { s.event(name) }

// Removed implements [internalos.DirWatcher].
func (s *stackApplierDebouncer) Removed(name string) { s.event(name) }

func (s *stackApplierDebouncer) event(name string) {
	if matches, _ := filepath.Match(manifestFilePattern, name); matches {
		select {
		case s.trigger <- struct{}{}:
		default:
		}
	}
}
