// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package applier

import (
	"errors"
	"os"
	"syscall"
)

// See man 2 inotify_init1
func handleCreateWatchErr(err error) (error, bool) {
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
			return &wrappedError{maxInotifyInstances + reached, err}, true
		} else if errors.Is(ferr, syscall.EMFILE) {
			return &wrappedError{maxFileDescriptors + reached, err}, false
		}

		return &wrappedError{maxInotifyInstances + " or " + maxFileDescriptors + reached, err}, true
	}

	return err, false
}

// See man 2 inotify_add_watch
func handleAddWatchErr(err error) (error, bool) {
	if errors.Is(err, syscall.ENOSPC) {
		return &wrappedError{"user limit on the total number of inotify watches was reached or the kernel failed to allocate a needed resource", err}, true
	}

	return err, false
}

type wrappedError struct {
	msg     string
	wrapped error
}

func (w *wrappedError) Error() string { return w.msg }
func (w *wrappedError) Unwrap() error { return w.wrapped }
