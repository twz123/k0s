// SPDX-FileCopyrightText: 2022 k0s authors
// SPDX-License-Identifier: Apache-2.0

package supervisor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"syscall"

	"github.com/k0sproject/k0s/internal/os/linux"
	"github.com/k0sproject/k0s/internal/os/linux/procfs"
	osunix "github.com/k0sproject/k0s/internal/os/unix"
	"golang.org/x/sys/unix"
)

type unixProcess struct {
	pid    int
	pidDir *osunix.Dir
}

func openPID(pid int) (_ *unixProcess, err error) {
	p := &unixProcess{pid: pid}
	p.pidDir, err = procfs.OpenPID(pid)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, syscall.ESRCH
		}
		return nil, err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, p.Close())
		}
	}()

	// The dir is open. It might refer to a thread, though.
	// Check if the thread group ID is the process ID.
	if status, err := p.dir().Status(); err != nil {
		return nil, err
	} else if tgid, err := status.ThreadGroupID(); err != nil {
		return nil, fmt.Errorf("failed to get thread group ID: %w", err)
	} else if tgid != pid {
		return nil, fmt.Errorf("%w (thread group ID is %d)", syscall.ESRCH, tgid)
	}

	return p, nil
}

func (p *unixProcess) Close() error {
	return p.pidDir.Close()
}

func (p *unixProcess) hasTerminated() (bool, error) {
	// Checking for termination is harder than one might think when there are
	// open file descriptors to that process. The "null signal" trick won't work
	// because the process remains a zombie as long as there are open file
	// descriptors to it. Rely on the proc filesystem once again to check if the
	// process has terminated or is a zombie.
	state, err := p.dir().State()
	if err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return true, nil
		}
		return false, err
	}

	return state == procfs.PIDStateZombie, nil
}

// cmdline implements [procHandle].
func (p *unixProcess) cmdline() ([]string, error) {
	cmdline, err := p.dir().Cmdline()
	if errors.Is(err, syscall.ESRCH) {
		return nil, os.ErrProcessDone
	}
	return cmdline, err
}

// environ implements [procHandle].
func (p *unixProcess) environ() ([]string, error) {
	env, err := p.dir().Environ()
	if errors.Is(err, syscall.ESRCH) {
		return nil, os.ErrProcessDone
	}
	return env, err
}

// requestGracefulTermination implements [procHandle].
func (p *unixProcess) requestGracefulTermination() error {
	if err := linux.SendSignal(p.pidDir, syscall.SIGTERM); errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	} else if !errors.Is(err, errors.ErrUnsupported) {
		return err
	}

	if err := syscall.Kill(p.pid, syscall.SIGTERM); errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	} else {
		return err
	}
}

// awaitTermination implements [procHandle]. Waits until the process terminated
// by using poll() on a pidfd. This is the only way that doesn't involve
// userspace polling using timeouts and such, but requires at least Linux 5.3.
func (p *unixProcess) awaitTermination(ctx context.Context) error {
	// In order to poll() on a PID file descriptor, it needs to be opened using
	// pidfd_open; the procfs file descriptors can't be used in that way.
	// https://www.man7.org/linux/man-pages/man2/pidfd_open.2.html
	pidFD, err := unix.PidfdOpen(p.pid, 0)
	if err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return nil // The PID doesn't exist anymore, nothing to wait for.
		}
		return os.NewSyscallError("pidfd_open", err)
	}
	defer func() { err = errors.Join(err, os.NewSyscallError("close", unix.Close(pidFD))) }()

	// Since the process has been opened via its PID, there might have been a
	// race. Check if the process is still alive. If it is, then it's guaranteed
	// that the PID hasn't been recycled in the meantime and both pidFD and p
	// are referring to the same process.
	if terminated, err := p.hasTerminated(); err != nil || terminated {
		return err
	}

	// Setup an eventfd object to wake up the poll call from a goroutine when
	// the context gets canceled.
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
		case <-ctx.Done():
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

		switch {
		case errors.Is(err, syscall.EINTR):
			continue

		case err != nil:
			return os.NewSyscallError("poll", err)

		case fds[0].Revents&unix.POLLIN != 0:
			return nil // the process has terminated

		case fds[1].Revents&unix.POLLIN != 0:
			return context.Cause(ctx) // the context has been canceled

		default:
			return fmt.Errorf("woke up unexpectedly (0x%x / 0x%x)", fds[0].Revents, fds[1].Revents)
		}
	}
}

func (p *unixProcess) dir() *procfs.PIDDir {
	return &procfs.PIDDir{FS: p.pidDir}
}
