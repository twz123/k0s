// SPDX-FileCopyrightText: 2020 k0s authors
// SPDX-License-Identifier: Apache-2.0

package applier

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/avast/retry-go"
	"github.com/k0sproject/k0s/pkg/k0scontext"
	"github.com/k0sproject/k0s/pkg/kubernetes"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/fsnotify/fsnotify"
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

type stackApplierPollOptions struct {
	force    bool
	interval time.Duration
}

// Run watches the stack for updates and executes the initial apply.
func (s *StackApplier) Run(ctx context.Context) error {
	err, usePolling := s.runWithWatch(ctx)
	if err != nil {
		pollInterval := 30 * time.Second
		if opts := k0scontext.Value[*stackApplierPollOptions](ctx); opts != nil {
			if opts.force {
				usePolling = true
			}
			pollInterval = opts.interval
		}

		if usePolling {
			s.log.WithError(err).Warn("Falling back to polling")
			return s.runWithPolling(ctx, pollInterval)
		}
	}

	return err
}

// Run watches the stack for updates and executes the initial apply.
func (s *StackApplier) runWithWatch(ctx context.Context) (error, bool) {
	if ctx.Err() != nil {
		return nil, false // The context is already done.
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		err, usePolling := handleCreateWatchErr(err)
		return fmt.Errorf("failed to create watcher: %w", err), usePolling
	}

	trigger := make(chan struct{}, 1)
	watchErr := make(chan error, 1)
	go func() { watchErr <- s.runWatcher(watcher, trigger, ctx.Done()) }()

	if err := watcher.Add(s.path); err != nil {
		err, usePolling := handleAddWatchErr(err)
		return fmt.Errorf("failed to watch %q: %w", s.path, err), usePolling
	}

	for {
		select {
		case <-trigger:
			s.apply(ctx)
		case err := <-watchErr:
			return err, false
		}
	}
}

func (s *StackApplier) runWatcher(watcher *fsnotify.Watcher, trigger chan<- struct{}, stop <-chan struct{}) (err error) {
	defer func() { err = errors.Join(err, watcher.Close()) }()

	const timeout = 1 * time.Second // debounce events for one second
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for {
		select {
		case err := <-watcher.Errors:
			return fmt.Errorf("while watching stack: %w", err)

		case event := <-watcher.Events:
			// Only consider events on manifest files
			if match, _ := filepath.Match(manifestFilePattern, filepath.Base(event.Name)); !match {
				continue
			}
			timer.Reset(timeout)

		case <-timer.C:
			select {
			case trigger <- struct{}{}:
			default:
			}

		case <-stop:
			return nil
		}
	}
}

type scannedFile struct {
	name    string
	size    int64
	modTime time.Time
}

func (s *StackApplier) runWithPolling(ctx context.Context, pollInterval time.Duration) error {
	if ctx.Err() != nil {
		return nil // The context is already done.
	}

	timer := time.NewTimer(0)
	defer timer.Stop()

	var applied []scannedFile

	for {
		s.log.Debug("Scanning manifest files")
		scanned, err := s.scanFiles(applied)
		if err != nil {
			s.log.WithError(err).Error("Failed to scan manifest files")
		} else if scanned != nil {
			s.apply(ctx)
			applied = scanned
		}

		timer.Reset(wait.Jitter(pollInterval, 0.2))

		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
		}
	}
}

func (s *StackApplier) scanFiles(prev []scannedFile) ([]scannedFile, error) {
	files, err := FindManifestFilesInDir(s.path)
	if err != nil {
		return nil, err
	}

	var changed bool
	current := make([]scannedFile, 0, len(files))
	for _, file := range files {
		stat, err := os.Stat(file)
		if err != nil {
			return nil, err
		}

		if !stat.Mode().IsRegular() {
			continue
		}

		path, err := filepath.Rel(s.path, file)
		if err != nil {
			return nil, err
		}

		file := scannedFile{
			name:    path,
			size:    stat.Size(),
			modTime: stat.ModTime(),
		}

		if !changed {
			if idx := len(current); idx >= len(prev) || prev[idx] != file {
				changed = true
			}
		}

		current = append(current, file)
	}

	if !changed && len(current) == len(prev) {
		return nil, nil
	}

	return current, nil
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
