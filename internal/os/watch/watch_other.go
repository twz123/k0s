//go:build !linux

// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package watch

import (
	"context"

	"github.com/fsnotify/fsnotify"
)

func watchDir(ctx context.Context, w *dirWatch) error {
	err, _ := w.runFSNotify(ctx)
	return err
}

func newFSNotifyWatcher() (*fsnotifyWatcher, error, bool) {
	watcher, err := fsnotify.NewWatcher()
	return (*fsnotifyWatcher)(watcher), err, false
}

func (w *fsnotifyWatcher) add(path string) (error, bool) {
	return (*fsnotify.Watcher)(w).Add(path), false
}
