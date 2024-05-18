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
	"sync/atomic"
	"syscall"

	"github.com/k0sproject/k0s/pkg/supervisor/procfs"

	"golang.org/x/sys/unix"
)

type handle struct {
	pid    PID
	closed chan struct{}
	d      atomic.Pointer[procfs.PIDFD]
}

// Linux specific implementation of [OpenHandle].
func openHandle(pid PID) (Handle, error) {
	pidFD, err := procfs.OpenPID(uint(pid))
	if err == nil {
		h := &handle{
			pid:    pid,
			closed: make(chan struct{}),
		}
		h.d.Store(pidFD)
		return h, err
	}
	if errors.Is(err, fs.ErrNotExist) {
		err = ErrNotExist
	}
	return nil, err
}

// Close implements [Handle].
func (h *handle) Close() error {
	d := h.d.Swap(nil)
	if d == nil {
		return fs.ErrClosed
	}
	close(h.closed)
	return d.Close()
}

// Kill implements [Handle].
func (h *handle) Kill() error {
	return h.Signal(os.Kill)
}

// Signal implements [Handle].
func (h *handle) Signal(signal os.Signal) error {
	d := h.d.Load()
	if d == nil {
		return fs.ErrClosed
	}
	err := d.Signal(signal)
	// Signal returns ESRCH when the process is reaped.
	if errors.Is(err, syscall.ESRCH) {
		return ErrTerminated
	}
	return err
}

// Environ implements [Handle].
func (h *handle) Environ() ([]string, error) {
	d := h.d.Load()
	if d == nil {
		return nil, fs.ErrClosed
	}
	env, err := d.Dir().Environ()
	// Environ returns EACCES when the process is terminated but not yet reaped.
	if errors.Is(err, syscall.EACCES) {
		if terminated, err := h.IsTerminated(); err != nil {
			return nil, err
		} else if terminated {
			return nil, ErrTerminated
		}
	}
	// Environ returns ESRCH when the process is reaped.
	if errors.Is(err, syscall.ESRCH) {
		return nil, ErrTerminated
	}
	return env, err
}

// Wait implements [Handle]. Waits until the process terminated by using poll()
// on a pidfd. This is the only way that doesn't involve userspace polling using
// timeouts and such, but requires at least Linux 5.3.
func (h *handle) Wait() (err error) {
	d := h.d.Load()
	if d == nil {
		return fs.ErrClosed
	}

	// In order to poll() on a PID file descriptor, it needs to be opened using
	// pidfd_open; the procfs file descriptors can't be used in that way.
	// https://www.man7.org/linux/man-pages/man2/pidfd_open.2.html
	pidFD, err := unix.PidfdOpen(int(h.pid), 0)
	if err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return nil // The PID doesn't exist anymore, nothing to wait for.
		}
		return os.NewSyscallError("pidfd_open", err)
	}
	defer func() { err = errors.Join(err, os.NewSyscallError("close", unix.Close(pidFD))) }()

	// Since the process has been opened via its PID, there might have been a
	// race. Check if the procfs.PIDFD is still referring to a non-reaped
	// process. If it is, then it's guaranteed that the PID hasn't been recycled
	// in the meantime and both pidFD and h.d are referring to the same process.
	if err := d.Signal(syscall.Signal(0)); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return nil // Process reaped.
		}
		return err
	}

	// Setup an eventfd object to wake up the poll call from a goroutine when
	// the handle gets closed.
	// https://www.man7.org/linux/man-pages/man2/eventfd.2.html
	eventFD, err := unix.Eventfd(0, unix.EFD_CLOEXEC)
	if err != nil {
		return os.NewSyscallError("eventfd", err)
	}
	defer func() { err = errors.Join(err, os.NewSyscallError("close", unix.Close(eventFD))) }()

	exit, done := make(chan struct{}), make(chan error, 1)
	go func() {
		defer close(done)
		select {
		case <-h.closed:
			// eventfds accept an uint64 between 0 and 2^64-1.
			one := [8]byte{7: 1}
			_, err := unix.Write(eventFD, one[:])
			done <- os.NewSyscallError("write", err)
		case <-exit:
		}
	}()
	defer func() {
		close(exit)
		if doneErr := <-done; doneErr != nil {
			err = errors.Join(err, doneErr)
		}
	}()

	for {
		// https://www.man7.org/linux/man-pages/man2/poll.2.html
		fds := [2]unix.PollFd{
			{Fd: int32(pidFD), Events: unix.POLLIN},
			{Fd: int32(eventFD), Events: unix.POLLIN},
		}
		_, err := unix.Poll(fds[:], -1)
		if err != nil {
			if errors.Is(err, syscall.EINTR) {
				continue
			}
			return os.NewSyscallError("poll", err)
		}
		if fds[0].Revents&unix.POLLIN != 0 { // the process has terminated
			d := h.d.Load().Dir()
			if d == nil {
				return fs.ErrClosed
			}
			return nil
		}
		if fds[1].Revents&unix.POLLIN != 0 { // the handle has been closed
			return fs.ErrClosed
		}
		return fmt.Errorf("woke up unexpectedly (0x%x / 0x%x)", fds[0].Revents, fds[1].Revents)
	}
}

// IsTerminated implements [Handle].
func (h *handle) IsTerminated() (bool, error) {
	// Don't use "the null signal" trick here to probe if the process is
	// terminated, as this will not detect zombie processes. Zombie processes
	// are effectively terminated, but not yet reaped, i.e. some parent process
	// has still to wait on them to collect their wait statuses.
	// https://www.man7.org/linux/man-pages/man3/kill.3p.html

	d := h.d.Load().Dir()
	if d == nil {
		return false, fs.ErrClosed
	}
	state, err := d.State()
	if err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return true, nil // The process is gone.
		}
		return false, err
	}
	switch state {
	case procfs.PIDStateZombie, procfs.PIDStateDead, procfs.PIDStateDeadX:
		return true, nil
	default:
		return false, nil
	}
}
