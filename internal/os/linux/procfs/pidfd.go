//go:build linux

/*
Copyright 2024 k0s authors

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

package procfs

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"syscall"

	"github.com/k0sproject/k0s/internal/os/linux"
	unixfs "github.com/k0sproject/k0s/internal/os/unix/fs"
)

// A file descriptor pointing to a PID directory inside the proc(5) filesystem.
// It is a stable reference to a specific process, and may be used in several
// syscalls that accept PID file descriptors (a.k.a. pidfds).
//
// Since Linux 5.3, the pidfd_open(2) syscall would be the preferred way of
// obtaining PID file descriptors. They allow for polling and awaiting process
// termination, which the procfs based PID file descriptors don't. The problem
// with those is that there's no way of inspecting the process environment
// through those. There's some basic memory management support, intended to be
// used by OOM killers, and the commit message mentions that there might be more
// interfaces in the future. But for now, the only thing that allows for
// inspecting the process environment through pidfds is procfs.
//
// https://man7.org/linux/man-pages/man2/pidfd_open.2.html
// https://github.com/torvalds/linux/commit/32fcb426ec001cb6d5a4a195091a8486ea77e2df
// https://github.com/torvalds/linux/commit/7615d9e1780e26e0178c93c55b73309a5dc093d7
type PIDFD interface {
	// Sends a signal to the process. The calling process must either be in the
	// same PID namespace as the process referred to by this descriptor, or be
	// in an ancestor of that namespace. Might be unsupported by some operating
	// systems.
	Signal(signal os.Signal) error

	// Returns a PIDDir that uses this PIDFD as its filesystem. It will inherit
	// the stability guarantees of PID file descriptors, i.e. it won't
	// accidentally pick up other processes that may have inherited the PID from
	// a process that has already been terminated and waited on.
	Dir() *PIDDir

	unixfs.DirFD
}

type pidFD struct{ unixfs.DirFD }

// Opens a PIDFD for the given PID via its idiomatic filesystem path. The file
// descriptor is a stable reference to the process. If the process terminates
// and its PID is recycled, the descriptor will not point to the new process
// with the same PID, but will fail if used.
func OpenPID(pid uint) (PIDFD, error) {
	d, err := unixfs.OpenDir(idiomaticPIDDirPath(pid))
	if err != nil {
		return nil, err
	}

	// The directory is open. It might refer to a thread, though.
	// Check if the thread group ID is the process ID.
	if status, statusErr := (&PIDDir{d}).Status(); statusErr != nil {
		err = statusErr
	} else if tgid, tgidErr := status.ThreadGroupID(); tgidErr != nil {
		err = fmt.Errorf("failed to get thread group ID: %w", tgidErr)
	} else if tgid != pid {
		err = fmt.Errorf("%w (thread group ID is %d)", fs.ErrNotExist, tgid)
	}
	if err != nil {
		return nil, errors.Join(err, d.Close())
	}

	return &pidFD{d}, nil
}

// Opens a PIDFD for the given process via its idiomatic filesystem path. The
// file descriptor is a stable reference to the process. If the process
// terminates and its PID is recycled, the descriptor will not point to the new
// process with the same PID, but will fail if used.
func OpenProcess(p *os.Process) (PIDFD, error) {
	if p == nil {
		return nil, fs.ErrInvalid
	}

	d, err := OpenPID(uint(p.Pid))
	if err != nil {
		return nil, err
	}

	// Send "the null signal" to the process to probe if it's not yet waited on.
	// https://www.man7.org/linux/man-pages/man3/kill.3p.html.
	// This is necessary to ensure that the pidfd is actually pointing to the
	// same process, and there was no interim PID recycling going on.
	if err := p.Signal(syscall.Signal(0)); err != nil {
		return nil, errors.Join(err, d.Close())
	}

	return d, nil
}

// Sends a signal to the process. See [linux.Signal].
func (p *pidFD) Signal(signal os.Signal) error {
	return linux.Signal(p, signal)
}

func (p *pidFD) Dir() *PIDDir {
	if p == nil {
		return nil
	}
	return &PIDDir{FS: p.DirFD}
}
