//go:build !linux

// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package os

import (
	"context"

	"github.com/fsnotify/fsnotify"
)

func watchDir(ctx context.Context, w *dirWatch) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	if trigger, errors, err := w.startWatch(ctx); err != nil {
		return err
	} else {
		return w.runWatch(ctx, trigger, errors)
	}
}

func newFSNotifyWatcher() (*fsnotifyWatcher, error) {
	watcher, err := fsnotify.NewWatcher()
	return (*fsnotifyWatcher)(watcher), err
}

func (w *fsnotifyWatcher) add(path string) error {
	return (*fsnotify.Watcher)(w).Add(path)
}
