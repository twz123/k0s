/*
Copyright 2022 k0s authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package process

import (
	"errors"
	"io/fs"
	"os"
	"syscall"

	"github.com/k0sproject/k0s/pkg/supervisor/procfs"
)

type handle procfs.PIDFD

// Linux specific implementation of [OpenHandle].
func openHandle(pid PID) (Handle, error) {
	pidFD, err := procfs.OpenPID(uint(pid))
	if err == nil {
		return (*handle)(pidFD), nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		err = ErrPIDNotExist
	}
	return nil, err
}

// Close implements [Handle].
func (h *handle) Close() error {
	return (*procfs.PIDFD)(h).Close()
}

// Signal implements [Handle].
func (h *handle) Signal(signal os.Signal) error {
	return normalizeErr((*procfs.PIDFD)(h).Signal(signal))
}

// Environ implements [Handle].
func (h *handle) Environ() ([]string, error) {
	env, err := (*procfs.PIDFD)(h).Environ()
	return env, normalizeErr(err)
}

// IsDone implements [Handle].
func (h *handle) IsDone() (bool, error) {
	// Send "the null signal" to probe if the PID still exists.
	// https://www.man7.org/linux/man-pages/man3/kill.3p.html
	err := h.Signal(syscall.Signal(0))
	switch err { //nolint:errorlint // guarded by a unit test
	case nil:
		return false, nil
	case ErrGone:
		return true, nil
	default:
		return false, err
	}
}

func normalizeErr(err error) error {
	if errors.Is(err, syscall.ESRCH) {
		return ErrGone
	}

	return err
}
