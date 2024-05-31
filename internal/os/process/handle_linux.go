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
	"fmt"
	"io/fs"
	"os"
	"syscall"

	"github.com/k0sproject/k0s/internal/os/linux/procfs"
)

type handle struct {
	pid uint
}

// Linux specific implementation of [OpenPID].
func openPID(pid uint) (Handle, error) {
	h := handle{pid}
	if terminated, err := h.IsTerminated(); err != nil {
		return nil, err
	} else if terminated {
		return nil, ErrNotExist
	}

	return &h, nil
}

// Environ implements [Handle].
func (h *handle) Environ() ([]string, error) {
	env, err := procfs.NewPIDDIR(h.pid).Environ()
	// Environ returns EACCES when the process is terminated but not yet reaped.
	if errors.Is(err, syscall.EACCES) {
		if terminated, err := h.IsTerminated(); err != nil {
			return nil, err
		} else if terminated {
			return nil, ErrTerminated
		}
	}
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrTerminated
	}

	return env, err
}

// Signal implements [Handle].
func (h *handle) Signal(signal os.Signal) error {
	sig, ok := signal.(syscall.Signal)
	if !ok {
		return fmt.Errorf("%w: %v", errors.ErrUnsupported, signal)
	}

	err := syscall.Kill(int(h.pid), sig)
	// Kill returns ESRCH when the process is reaped.
	if errors.Is(err, syscall.ESRCH) {
		return ErrTerminated
	}
	return err
}

// Kill implements [Handle].
func (h *handle) Kill() error {
	return h.Signal(os.Kill)
}

// IsTerminated implements [Handle].
func (h *handle) IsTerminated() (bool, error) {
	// Use "the null signal" trick here to probe if the process is terminated.
	// Note that this will not detect zombie processes. Zombie processes are
	// effectively terminated, but not yet reaped, i.e. some parent process has
	// still to wait on them to collect their wait statuses.
	// https://www.man7.org/linux/man-pages/man3/kill.3p.html

	err := h.Signal(syscall.Signal(0))
	if err == nil {
		return false, nil
	}
	if errors.Is(err, ErrTerminated) {
		return true, nil
	}
	return false, err
}
