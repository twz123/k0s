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
	ctx = internallog.AttachToContext(ctx, s.log)
	return internalos.WatchDir(ctx, s.path,
		func(event internalos.PathWatchEvent) bool {
			var visitor stackApplierWatchEventVisitor
			event.Accept(&visitor)
			return visitor.accepted
		},
		1*time.Second,
		func(ctx context.Context) error {
			s.apply(ctx)
			return nil
		},
	)
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

type stackApplierWatchEventVisitor struct {
	accepted bool
}

// Changed implements [internalos.PathWatchEventVisitor].
func (s *stackApplierWatchEventVisitor) Changed(name string, info func() (fs.FileInfo, error)) {
	s.accepted, _ = filepath.Match(manifestFilePattern, name)
}

// Removed implements [internalos.PathWatchEventVisitor].
func (s *stackApplierWatchEventVisitor) Removed(name string) {
	s.accepted, _ = filepath.Match(manifestFilePattern, name)
}
