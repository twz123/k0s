// SPDX-FileCopyrightText: 2022 k0s authors
// SPDX-License-Identifier: Apache-2.0

package supervisor

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/k0sproject/k0s/internal/os/unix"
)

type unixProcess struct {
	pid      int
	pidDirFD *unix.DirFD
}

func openPID(pid int) (_ *unixProcess, err error) {
	pidDirFD, err := unix.OpenDir(filepath.Join("/proc", strconv.Itoa(pid)), 0)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, syscall.ESRCH
		}
		return nil, err
	}
	return &unixProcess{pid, pidDirFD}, nil
}

func (p *unixProcess) Close() error {
	return p.pidDirFD.Close()
}

func (p *unixProcess) hasTerminated() (bool, error) {
	// Checking for termination is harder than one might think when there are
	// open file descriptors to that process. The "null signal" trick won't work
	// because the process remains a zombie as long as there are file
	// descriptors to it. Rely on the proc filesystem once again to check if the
	// process has terminated or is a zombie.
	if zombie, err := p.isZombie(); err != nil {
		if errors.Is(err, os.ErrProcessDone) {
			return false, nil
		}
		return false, err
	} else if zombie {
		return true, nil
	}

	return false, nil
}

// cmdline implements [procHandle].
func (p *unixProcess) cmdline() ([]string, error) {
	cmdline, err := p.pidDirFD.ReadFile("cmdline")
	if err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return nil, os.ErrProcessDone
		}
		return nil, err
	}

	return strings.Split(string(cmdline), "\x00"), nil
}

// environ implements [procHandle].
func (p *unixProcess) environ() ([]string, error) {
	env, err := p.pidDirFD.ReadFile("environ")
	if err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return nil, os.ErrProcessDone
		}
	}

	return strings.Split(string(env), "\x00"), nil
}

// requestGracefulTermination implements [procHandle].
func (p *unixProcess) requestGracefulTermination() error {
	// Use Go's process API to send a signal. On modern kernels (since Linux
	// 5.3), it will use the pidfd_open syscall to obtain a pidfd to be used
	// with the pidfd_send_signal syscall. This avoids race conditions that
	// occur when using the traditional kill syscall. The problem is that kill
	// specifies the target process via a PID. As a consequence, the sender may
	// accidentally send a signal to the wrong process if the originally
	// intended target process has terminated and its PID has been recycled for
	// another process. By contrast, a pidfd is a stable reference to a specific
	// process which needs to be closed. If that process terminated in the
	// meantime, pidfd_send_signal will fail with ESRCH, no matter what happened
	// to the PID.

	process, _ := os.FindProcess(p.pid) // On UNIX, FindProcess is infallible.
	defer func() { _ = process.Release() }()

	// Make a check against the PID dirfd: If that dirfd still references an
	// existing process ...
	if _, err := p.pidDirFD.StatAt("stat", 0); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	// ... and the kernel supports pidfd_open, we now have a pidfd that is
	// guaranteed to point to the same process as the PID dirfd.

	// This is as much as we can do.
	// On older kernels, there's simply no race-free way to do this.
	return requestGracefulTermination(process)
}

func (p *unixProcess) isZombie() (bool, error) {
	// https://man7.org/linux/man-pages/man5/proc_pid_stat.5.html
	stat, err := p.pidDirFD.ReadFile("stat")
	if err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return false, os.ErrProcessDone
		}
		return false, err
	}

	// Discard the pid and comm fields: The last parenthesis marks the end of
	// the comm field, all other fields won't contain parentheses. The end of
	// comm needs to be at the fourth byte the earliest.
	if endOfComm := bytes.LastIndex(stat, []byte{')'}); endOfComm < 3 {
		return false, errors.New("/proc/[pid]/stat malformed")
	} else {
		stat = stat[endOfComm+1:]
	}

	// Parse the state field. It's a single character.
	if len(stat) < 3 || stat[0] != ' ' || stat[2] != ' ' {
		return false, errors.New("/proc/[pid]/stat malformed")
	}

	return stat[1] == 'Z', nil
}
