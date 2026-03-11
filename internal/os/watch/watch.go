// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package watch

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"
)

type fsnotifyWatcher fsnotify.Watcher

func (w *fsnotifyWatcher) Close() error { return (*fsnotify.Watcher)(w).Close() }

type DirWatcher interface {
	Activated(path string)
	Touched(name string, info func() (fs.FileInfo, error)) // FIXME: do we need the info func?
	Removed(name string)
}

type DirWatchEvent interface {
	Accept(DirWatcher)
}

type DirWatchEventPredicate func(DirWatchEvent) bool

func DenyActivations() DirWatchEventPredicate {
	return func(e DirWatchEvent) bool {
		var denied bool
		funcs := DirWatcherFuncs{
			OnActivated: func(string) { denied = true },
		}
		e.Accept(&funcs)
		return !denied
	}
}

func DenyNames(deny func(string) bool) DirWatchEventPredicate {
	return func(e DirWatchEvent) bool {
		var denied bool
		funcs := DirWatcherFuncs{
			OnTouched: func(name string, _ func() (fs.FileInfo, error)) { denied = deny(name) },
			OnRemoved: func(name string) { denied = deny(name) },
		}
		e.Accept(&funcs)
		return !denied
	}
}

type DirWatchEventVisitor interface {
	Visit(e DirWatchEvent)
}

type DirWatchEventVisitorFunc func(e DirWatchEvent)

func (f DirWatchEventVisitorFunc) Visit(e DirWatchEvent) { f(e) }

type DirWatcherFuncs struct {
	OnActivated func(path string)
	OnRemoved   func(name string)
	OnTouched   func(name string, info func() (fs.FileInfo, error))
}

// Visit implements [DirWatchEventVisitor].
func (f *DirWatcherFuncs) Visit(e DirWatchEvent) {
	e.Accept(f)
}

// Activated implements [DirWatcher].
func (f *DirWatcherFuncs) Activated(path string) {
	if f.OnActivated != nil {
		f.OnActivated(path)
	}
}

// Touched implements [DirWatcher].
func (f *DirWatcherFuncs) Touched(name string, info func() (fs.FileInfo, error)) {
	if f.OnTouched != nil {
		f.OnTouched(name, info)
	}
}

// Removed implements [DirWatcher].
func (f *DirWatcherFuncs) Removed(name string) {
	if f.OnRemoved != nil {
		f.OnRemoved(name)
	}
}

// WatchDir watches a directory and invokes onEvent for each observed event.
//
// On Linux, watcher initialization failures related to inotify/fd limits can
// fall back to polling.
func WatchDir(ctx context.Context, path string, visitor DirWatchEventVisitor) error {
	return watchDir(ctx, &dirWatch{path: path, visitor: visitor})
}

type OnDirChange struct {
	InitialDelay time.Duration
	Delay        time.Duration
	Accepts      DirWatchEventPredicate
}

func (opts OnDirChange) Run(ctx context.Context, path string, onChange func(context.Context) error) (err error) {
	ctx, cancel := context.WithCancelCause(ctx)
	defer func() { cancel(err) }()

	timer := time.NewTimer(0)
	defer timer.Stop()

	var (
		lastActivity atomic.Pointer[time.Time]
		watchErr     error
	)

	changed := make(chan struct{}, 1)
	go func() {
		defer close(changed)
		watchErr = WatchDir(ctx, path, DirWatchEventVisitorFunc(func(e DirWatchEvent) {
			if opts.Accepts == nil || opts.Accepts(e) {
				now := time.Now()
				lastActivity.Store(&now)
				select {
				case changed <- struct{}{}:
				default:
				}
			}
		}))
	}()

waitForChange:
	for initial := true; ; {
		select {
		case _, ok := <-changed:
			if !ok {
				break waitForChange
			}

			for {
				delay := opts.Delay
				if initial {
					delay = opts.InitialDelay
				}
				timer.Reset(delay - time.Since(*lastActivity.Load()))

				select {
				case <-timer.C:
					select {
					case _, ok := <-changed:
						if !ok {
							break waitForChange
						}
					default:
						if err := onChange(ctx); err != nil {
							return err
						}
						initial = false
						continue waitForChange
					}

				case <-ctx.Done():
					return nil
				}
			}

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

type dirWatch struct {
	path    string
	visitor DirWatchEventVisitor
}

type dirWatchEventFunc func(DirWatcher)

func (f dirWatchEventFunc) Accept(w DirWatcher) { f(w) }

func (w *dirWatch) fire(f func(DirWatcher)) {
	w.visitor.Visit(dirWatchEventFunc(f))
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

	w.fire(func(watcher DirWatcher) {
		watcher.Activated(w.path)
	})

	for {
		select {
		case event := <-watcher.Events:
			name := filepath.Base(event.Name)
			switch {
			case event.Has(fsnotify.Remove):
				if event.Name == w.path {
					return errors.New("watched directory has been removed"), false
				}
				w.fire(func(w DirWatcher) {
					w.Removed(name)
				})

			case event.Has(fsnotify.Rename):
				if event.Name == w.path {
					return errors.New("watched directory has been renamed"), false
				}
				w.fire(func(w DirWatcher) {
					w.Removed(name)
				})

			case event.Has(fsnotify.Create), event.Has(fsnotify.Write), event.Has(fsnotify.Chmod):
				w.fire(func(w DirWatcher) {
					w.Touched(name, sync.OnceValues(func() (fs.FileInfo, error) {
						return os.Stat(event.Name)
					}))
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
