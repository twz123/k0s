// SPDX-FileCopyrightText: 2020 k0s authors
// SPDX-License-Identifier: Apache-2.0

package applier

import (
	"cmp"
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"sync"
	"time"

	internalos "github.com/k0sproject/k0s/internal/os"
	internallog "github.com/k0sproject/k0s/internal/pkg/log"
	"github.com/k0sproject/k0s/internal/sync/value"
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
	var (
		watcher  stackDirWatcher
		watchErr error
	)

	_, changed := watcher.lastActivity.Peek()
	done := make(chan struct{})
	go func() {
		defer close(done)
		ctx := internallog.AttachToContext(ctx, s.log)
		watchErr = internalos.WatchDir2(ctx, s.path, &watcher)
	}()

	timer := time.NewTimer(0)
	defer timer.Stop()

watch:
	for {
		select {
		case <-changed:
			var lastActivity time.Time
			lastActivity, changed = watcher.lastActivity.Peek()
			timer.Reset(1*time.Second - time.Since(lastActivity))

			select {
			case <-timer.C:
				select {
				case <-changed:
					continue
				default:
					s.apply(ctx)
				}

			case <-done:
				break watch

			case <-ctx.Done():
				return nil
			}

		case <-done:
			break watch

		case <-ctx.Done():
			return nil
		}
	}

	select {
	case <-ctx.Done():
		return nil
	default:
		return cmp.Or(watchErr, errors.New("watch terminated unexpectedly"))
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

type stackDirWatcher struct {
	lastActivity value.WriteOptimizedLatest[time.Time]
}

// Activated implements [internalos.DirWatcher].
func (w *stackDirWatcher) Activated(string) {
	w.lastActivity.Set(time.Now())
}

// Touched implements [internalos.DirWatcher].
func (w *stackDirWatcher) Touched(name string, _ func() (fs.FileInfo, error)) { w.event(name) }

// Removed implements [internalos.DirWatcher].
func (w *stackDirWatcher) Removed(name string) { w.event(name) }

func (w *stackDirWatcher) event(name string) {
	if matches, _ := filepath.Match(manifestFilePattern, name); matches {
		w.lastActivity.Set(time.Now())
	}
}
