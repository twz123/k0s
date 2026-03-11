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

// Dir watches a directory and invokes visitor for each observed event.
//
// On Linux, watcher initialization failures related to inotify/fd limits can
// fall back to polling.
func Dir(ctx context.Context, path string, visitor Visitor) error {
	return watchDir(ctx, &dirWatch{path: path, visitor: visitor})
}

type OnDirChange struct {
	InitialDelay time.Duration
	Delay        time.Duration
	Accepts      EventPredicate
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
		watchErr = Dir(ctx, path, VisitorFunc(func(e Event) {
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
	visitor Visitor
}

type eventFunc func(Watcher)

func (f eventFunc) Accept(w Watcher) { f(w) }

func (d *dirWatch) fire(f func(Watcher)) {
	d.visitor.Visit(eventFunc(f))
}

func (d *dirWatch) runFSNotify(ctx context.Context) (error, bool) {
	watcher, err, fallback := newFSNotifyWatcher()
	if err != nil {
		return fmt.Errorf("failed to create watcher: %w", err), fallback
	}
	defer func() { err = errors.Join(err, watcher.Close()) }()

	if err, fallback := watcher.add(d.path); err != nil {
		return fmt.Errorf("failed to watch: %w", err), fallback
	}

	d.fire(func(w Watcher) {
		w.Activated(d.path)
	})

	for {
		select {
		case event := <-watcher.Events:
			name := filepath.Base(event.Name)
			switch {
			case event.Has(fsnotify.Remove):
				if event.Name == d.path {
					return errors.New("watched directory has been removed"), false
				}
				d.fire(func(w Watcher) {
					w.Removed(name)
				})

			case event.Has(fsnotify.Rename):
				if event.Name == d.path {
					return errors.New("watched directory has been renamed"), false
				}
				d.fire(func(w Watcher) {
					w.Removed(name)
				})

			case event.Has(fsnotify.Create), event.Has(fsnotify.Write), event.Has(fsnotify.Chmod):
				d.fire(func(w Watcher) {
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
