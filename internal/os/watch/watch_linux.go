// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package watch

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
	err, fallback := w.runFSNotify(ctx)
	if err == nil || !fallback {
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
	return w.runPolling(ctx, log, pollInterval)
}

func newFSNotifyWatcher() (*fsnotifyWatcher, error, bool) {
	watcher, err := fsnotify.NewWatcher()
	if err == nil {
		return (*fsnotifyWatcher)(watcher), nil, false
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
			return nil, &fsnotifyError{maxInotifyInstances + reached, err}, true
		} else if errors.Is(ferr, syscall.EMFILE) {
			return nil, &fsnotifyError{maxFileDescriptors + reached, err}, false
		}

		return nil, &fsnotifyError{maxInotifyInstances + " or " + maxFileDescriptors + reached, err}, true
	}

	return nil, err, false
}

func (w *fsnotifyWatcher) add(path string) (error, bool) {
	err := (*fsnotify.Watcher)(w).AddWith(path)
	if err == nil {
		return nil, false
	}

	// See man 2 inotify_add_watch
	if errors.Is(err, syscall.ENOSPC) {
		return &fsnotifyError{"user limit on the total number of inotify watches was reached or the kernel failed to allocate a needed resource", err}, true
	}

	return err, false
}

type fsnotifyError struct {
	msg     string
	wrapped error
}

func (w *fsnotifyError) Error() string { return w.msg }
func (w *fsnotifyError) Unwrap() error { return w.wrapped }

type polledDirEntry struct {
	name    string
	size    int64
	modTime time.Time
}

func (w *dirWatch) runPolling(ctx context.Context, log logrus.FieldLogger, pollInterval time.Duration) error {
	if ctx.Err() != nil {
		return nil // The context is already done.
	}

	timer := time.NewTimer(0)
	defer timer.Stop()

	polled, err := pollDirEntries(w.path, nil, nil)
	if err != nil {
		return fmt.Errorf("initial poll failed: %w", err)
	}

	w.fire(func(watcher DirWatcher) {
		watcher.Activated(w.path)
	})

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
			log.Debug("Polling for changes")
			var err error
			polled, err = pollDirEntries(w.path, polled, w.fire)
			if err != nil {
				return fmt.Errorf("failed to poll: %w", err)
			}
			timer.Reset(wait.Jitter(pollInterval, 0.2))
		}
	}
}

func pollDirEntries(path string, prev []polledDirEntry, fire func(func(DirWatcher))) ([]polledDirEntry, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}

	current := make([]polledDirEntry, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, err
		}

		current = append(current, polledDirEntry{
			name:    entry.Name(),
			size:    info.Size(),
			modTime: info.ModTime(),
		})

		var (
			unchanged bool
			i         uint
		)
		for prevLen := uint(len(prev)); i < prevLen; i++ {
			name := prev[i].name
			if cmp := strings.Compare(name, entry.Name()); cmp < 0 {
				if fire != nil {
					fire(func(w DirWatcher) {
						w.Removed(name)
					})
				}
			} else if cmp == 0 {
				unchanged = prev[i].size == info.Size() && prev[i].modTime.Equal(info.ModTime())
				i++
				break
			} else {
				break
			}
		}
		prev = prev[i:]

		if !unchanged && fire != nil {
			name := entry.Name()
			fire(func(w DirWatcher) {
				w.Touched(name, func() (fs.FileInfo, error) { return info, nil })
			})
		}
	}

	if fire != nil {
		for i := range prev {
			name := prev[i].name
			fire(func(w DirWatcher) {
				w.Removed(name)
			})
		}
	}

	return current, nil
}
