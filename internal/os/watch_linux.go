// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package os

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/k0sproject/k0s/pkg/k0scontext"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/wait"
)

func watchDir(ctx context.Context, w *dirWatch) error {
	err, usePolling := w.runWithWatch(ctx)
	if err == nil && !usePolling {
		return err
	}

	log := k0scontext.ValueOrElse(ctx, func() logrus.FieldLogger {
		return logrus.StandardLogger().WithFields(logrus.Fields{
			"component": "os/watch",
			"path":      w.path,
		})
	})

	const pollInterval = 30 * time.Second
	log.WithError(err).Warn("Falling back to polling every ", pollInterval)
	return w.runWithPolling(ctx, log, pollInterval)
}

func (w *dirWatch) runWithWatch(ctx context.Context) (error, bool) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var err error
	if trigger, errors, startErr := w.startWatch(ctx); startErr == nil {
		return w.runWatch(ctx, trigger, errors), false
	} else {
		err = startErr
	}

	var fsnotifyErr *fsnotifyError
	return err, errors.As(err, &fsnotifyErr) && fsnotifyErr.usePolling
}

type polledDirEntry struct {
	name    string
	size    int64
	modTime time.Time
}

func (w *dirWatch) runWithPolling(ctx context.Context, log logrus.FieldLogger, pollInterval time.Duration) error {
	if ctx.Err() != nil {
		return nil // The context is already done.
	}

	timer := time.NewTimer(0)
	defer timer.Stop()

	var (
		polled        []polledDirEntry
		pendingChange bool
	)

	for {
		log.Debug("Polling for changes")

		var (
			hasChangeNow bool
			err          error
		)

		polled, hasChangeNow, err = w.pollDirEntries(polled)
		if err != nil {
			return fmt.Errorf("failed to poll: %w", err)
		} else if hasChangeNow {
			pendingChange = true
		} else if pendingChange && !hasChangeNow {
			pendingChange = false
			if err := w.onChange(ctx); err != nil {
				return err
			}
		}

		if pendingChange {
			timer.Reset(w.delay)
		} else {
			timer.Reset(wait.Jitter(pollInterval, 0.2))
		}

		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
		}
	}
}

func (w *dirWatch) pollDirEntries(prev []polledDirEntry) ([]polledDirEntry, bool, error) {
	files, err := os.ReadDir(w.path)
	if err != nil {
		return nil, false, err
	}

	var hasAcceptedChange bool
	current := make([]polledDirEntry, 0, len(files))
	for _, file := range files {
		info, err := file.Info()
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, false, err
		}

		current = append(current, polledDirEntry{
			name:    file.Name(),
			size:    info.Size(),
			modTime: info.ModTime(),
		})

		if !hasAcceptedChange {
			var unchanged bool
			for i := range prev {
				p := &prev[i]
				if cmp := strings.Compare(p.name, file.Name()); cmp < 0 {
					if !w.accept(PathWatchEventFunc(func(v PathWatchEventVisitor) {
						v.Removed(p.name)
					})) {
						continue
					}

					hasAcceptedChange = true
				} else if cmp == 0 {
					unchanged = p.size == info.Size() && p.modTime.Equal(info.ModTime())
					prev = prev[i+1:]
				} else {
					prev = prev[i:]
				}
				break
			}

			if !hasAcceptedChange && !unchanged {
				hasAcceptedChange = w.accept(PathWatchEventFunc(func(v PathWatchEventVisitor) {
					v.Changed(file.Name(), func() (fs.FileInfo, error) { return info, nil })
				}))
			}
		}
	}

	if !hasAcceptedChange {
		for i := range prev {
			name := prev[i].name
			if w.accept(PathWatchEventFunc(func(v PathWatchEventVisitor) {
				v.Removed(name)
			})) {
				return current, true, nil
			}
		}
	}

	return current, hasAcceptedChange, nil
}

func newFSNotifyWatcher() (*fsnotifyWatcher, error) {
	watcher, err := fsnotify.NewWatcher()
	if err == nil {
		return (*fsnotifyWatcher)(watcher), nil
	}

	// See man 2 inotify_init1
	if errors.Is(err, syscall.EMFILE) {
		// This may occur if the number of open inotify watches per user exceeds
		// the fs.inotify.max_user_instances sysctl setting or if the maximum
		// number of open files per process has been reached. These two
		// conditions share the same error code and cannot be distinguished by
		// the caller without further investigation.

		const (
			maxInotifyInstances = "user limit on the total number of inotify instances"
			maxFileDescriptors  = "per-process limit on the number of open file descriptors"
			reached             = " has been reached"
		)

		if f, ferr := os.Open("/dev/null"); ferr == nil {
			_ = f.Close()
			return nil, &fsnotifyError{maxInotifyInstances + reached, true, err}
		} else if errors.Is(ferr, syscall.EMFILE) {
			return nil, &fsnotifyError{maxFileDescriptors + reached, false, err}
		}

		return nil, &fsnotifyError{maxInotifyInstances + " or " + maxFileDescriptors + reached, true, err}
	}

	return nil, err
}

func (w *fsnotifyWatcher) add(path string) error {
	err := (*fsnotify.Watcher)(w).Add(path)
	if err == nil {
		return nil
	}

	// See man 2 inotify_add_watch
	if errors.Is(err, syscall.ENOSPC) {
		return &fsnotifyError{"user limit on the total number of inotify watches was reached or the kernel failed to allocate a needed resource", true, err}
	}

	return err
}

type fsnotifyError struct {
	msg        string
	usePolling bool
	wrapped    error
}

func (w *fsnotifyError) Error() string { return w.msg }
func (w *fsnotifyError) Unwrap() error { return w.wrapped }
