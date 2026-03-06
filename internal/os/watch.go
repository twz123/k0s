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
	"strconv"
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

type WatchPollFallback uint8

const (
	WatchPollFallbackDefault WatchPollFallback = iota
	WatchPollFallbackNever
	WatchPollFallbackAlways
)

func (f WatchPollFallback) String() string {
	switch f {
	case WatchPollFallbackDefault:
		return "WatchPollFallbackDefault"
	case WatchPollFallbackNever:
		return "WatchPollFallbackNever"
	case WatchPollFallbackAlways:
		return "WatchPollFallbackAlways"
	default:
		return strconv.Itoa(int(f))
	}
}

type PathWatchOptions struct {
	Delay        time.Duration
	PollFallback WatchPollFallback
}

type watchPollOptions struct {
	force    bool
	interval time.Duration
}

// WatchDir watches a directory and invokes watcher's methods for each observed
// event.
//
// On Linux, watcher initialization failures related to inotify/fd limits can
// fall back to polling.
func WatchDir(ctx context.Context, path string, watcher DirWatcher) error {
	return watchDir(ctx, &dirWatch{path: path, watcher: watcher})
}

type dirWatch struct {
	path    string
	watcher DirWatcher
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

	for {
		select {
		case event := <-watcher.Events:
			name := filepath.Base(event.Name)
			switch event.Op {
			case fsnotify.Create, fsnotify.Write, fsnotify.Chmod:
				w.watcher.Touched(name, func() (fs.FileInfo, error) {
					return os.Stat(event.Name)
				})

			case fsnotify.Remove:
				if event.Name == w.path {
					return errors.New("watched directory has been removed"), false
				}
				w.watcher.Removed(name)

			case fsnotify.Rename:
				if event.Name == w.path {
					return errors.New("watched directory has been renamed"), false
				}
				w.watcher.Removed(name)
			}

		case err := <-watcher.Errors:
			return fmt.Errorf("while watching: %w", err), false
		case <-ctx.Done():
			return nil, false
		}
	}
}
