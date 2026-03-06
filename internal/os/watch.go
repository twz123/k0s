// SPDX-FileCopyrightText: 2020 k0s authors
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

type PathWatchEventVisitor interface {
	Changed(name string, info func() (fs.FileInfo, error)) // FIXME: do we need the info func?
	Removed(name string)
}

type PathWatchEvent interface {
	Accept(PathWatchEventVisitor)
}

type PathWatchEventFunc func(PathWatchEventVisitor)

func (f PathWatchEventFunc) Accept(v PathWatchEventVisitor) { f(v) }

// Watches a directory and invokes onChange after accepted changes have quiesced
// for at least delay.
//
// The accept callback filters candidate events. When it returns true for one
// or more events, WatchDir waits until no further accepted events are seen for
// delay and then calls onChange once.
//
// On Linux, watcher initialization failures related to inotify/fd limits can
// fall back to polling.
func WatchDir(ctx context.Context, path string, accept func(PathWatchEvent) bool, delay time.Duration, onChange func(context.Context) error) error {
	return watchDir(ctx, &dirWatch{
		path:     path,
		accept:   accept,
		delay:    delay,
		onChange: onChange,
	})
}

type dirWatch struct {
	path     string
	accept   func(event PathWatchEvent) bool
	delay    time.Duration
	onChange func(ctx context.Context) error // FIXME: is the error return needed?
}

// FIXME: maybe inline?
var (
	errCreateWatcher = errors.New("failed to create watcher")
	errWatch         = errors.New("failed to watch")
)

func (w *dirWatch) startWatch(ctx context.Context) (<-chan struct{}, <-chan error, error) {
	watcher, err := newFSNotifyWatcher()
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", errCreateWatcher, err)
	}

	trigger, errors := make(chan struct{}, 1), make(chan error, 1)
	go func() { errors <- w.runWatcher(watcher, trigger, ctx.Done()) }()

	if err := watcher.add(w.path); err != nil {
		return nil, nil, fmt.Errorf("%w: %w", errWatch, err)
	}

	return trigger, errors, nil
}

func (w *dirWatch) runWatch(ctx context.Context, trigger <-chan struct{}, errors <-chan error) error {
	for {
		select {
		case <-trigger:
			if err := w.onChange(ctx); err != nil {
				return err
			}
		case err := <-errors:
			return err
		}
	}
}

func (w *dirWatch) runWatcher(watcher *fsnotifyWatcher, trigger chan<- struct{}, stop <-chan struct{}) (err error) {
	defer func() { err = errors.Join(err, watcher.Close()) }()

	timer := time.NewTimer(w.delay)
	defer timer.Stop()

	for {
		select {
		case err := <-watcher.Errors:
			return fmt.Errorf("while watching: %w", err)

		case event := <-watcher.Events:
			if !w.accept((*fsnotifyWatchEvent)(&event)) {
				continue
			}

			timer.Reset(w.delay)

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

type fsnotifyWatchEvent fsnotify.Event

// Accept implements [PathWatchEvent].
func (e *fsnotifyWatchEvent) Accept(v PathWatchEventVisitor) {
	switch e.Op {
	case fsnotify.Remove:
		v.Removed(filepath.Base(e.Name))
	default:
		v.Changed(filepath.Base(e.Name), func() (fs.FileInfo, error) { return os.Stat(e.Name) })
	}
}
