// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package watch

import (
	"errors"
	"os"
	"syscall"

	"github.com/fsnotify/fsnotify"
)

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
