// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package os

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/fsnotify/fsnotify"
)

type fsnotifyWatcher fsnotify.Watcher

func (w *fsnotifyWatcher) Close() error { return (*fsnotify.Watcher)(w).Close() }

type DirWatcher interface {
	Touched(name string, info func() (fs.FileInfo, error)) // FIXME: do we need the info func?
	Removed(name string)
}

type NoopDirWatcher struct{}

func (*NoopDirWatcher) Touched(string, func() (fs.FileInfo, error)) {}
func (*NoopDirWatcher) Removed(string)                              {}

// WatchDir watches a directory and invokes watcher's methods for each observed
// event.
//
// On Linux, watcher initialization failures related to inotify/fd limits can
// fall back to polling.
func WatchDir(ctx context.Context, path string, initialList bool, watcher DirWatcher) error {
	return watchDir(ctx, &dirWatch{path: path, initialList: initialList, watcher: watcher})
}

type dirWatch struct {
	path        string
	initialList bool
	watcher     DirWatcher
}

func (w *dirWatch) runFSNotify(ctx context.Context) (error, bool) {
	watcher, err, fallback := newFSNotifyWatcher()
	if err != nil {
		return fmt.Errorf("failed to create watcher: %w", err), fallback
	}
	defer func() { err = errors.Join(err, watcher.Close()) }()

	if err, fallback := watcher.add(w.path); err != nil {
		return fmt.Errorf("failed to watch: %w", err), fallback
	}

	// List all directory entries after the path has been added to the watcher.
	// Doing it the other way round introduces a race condition when entries get
	// created after the initial listing but before the watch starts.

	if w.initialList {
		entries, err := os.ReadDir(w.path)
		if err != nil {
			return fmt.Errorf("failed to list: %w", err), false
		}

		for _, entry := range entries {
			w.watcher.Touched(entry.Name(), entry.Info)
		}
	}

	for {
		select {
		case event := <-watcher.Events:
			name := filepath.Base(event.Name)
			switch {
			case event.Has(fsnotify.Remove):
				if event.Name == w.path {
					return errors.New("watched directory has been removed"), false
				}
				w.watcher.Removed(name)

			case event.Has(fsnotify.Rename):
				if event.Name == w.path {
					return errors.New("watched directory has been renamed"), false
				}
				w.watcher.Removed(name)

			case event.Has(fsnotify.Create), event.Has(fsnotify.Write), event.Has(fsnotify.Chmod):
				w.watcher.Touched(name, func() (fs.FileInfo, error) {
					return os.Stat(event.Name)
				})

			default:
				return fmt.Errorf("unknown event: %v", event), false
			}

		case err := <-watcher.Errors:
			return fmt.Errorf("while watching: %w", err), false
		case <-ctx.Done():
			return nil, false
		}
	}
}
