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
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

const procMount = "/proc"

// A file descriptor that refers to a process, a.k.a. a pidfd.
type PIDFD struct {
	mu sync.Mutex

	// A file backed by the pidfd that refers to the process.
	procFile *os.File
}

// Linux specific implementation of [OpenHandle].
func openHandle(pid PID) (Handle, error) {
	return OpenPIDFD(pid)
}

func OpenPIDFD(pid PID) (*PIDFD, error) {
	f, err := procfsPidfdOpen(pid)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrPIDNotExist
		}
		return nil, err
	}

	return &PIDFD{procFile: f}, nil
}

// Close implements [Handle].
func (p *PIDFD) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.procFile == nil {
		return syscall.EINVAL
	}

	err := p.procFile.Close()
	p.procFile = nil
	return err
}

// Signal implements [Handle].
func (p *PIDFD) Signal(signal os.Signal) error {
	sig, ok := signal.(syscall.Signal)
	if !ok {
		return fmt.Errorf("unsupported signal: %v", signal)
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if p.procFile == nil {
		return syscall.EINVAL
	}

	err := pidfdSendSignal(int(p.procFile.Fd()), sig)
	if errors.Is(err, syscall.ESRCH) {
		return ErrGone
	}

	return err
}

// Environ implements [Handle].
func (p *PIDFD) Environ() ([]string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.procFile == nil {
		return nil, syscall.EINVAL
	}

	envBlock, err := p.readEnvBlock()
	if err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return nil, ErrGone
		}

		return nil, err
	}

	return parseEnvBlock(envBlock), nil
}

// IsDone implements [Handle].
func (p *PIDFD) IsDone() (bool, error) {
	// Send "the null signal" to probe if the PID still exists.
	// https://www.man7.org/linux/man-pages/man3/kill.3p.html
	err := p.Signal(syscall.Signal(0))
	switch err { //nolint:errorlint // guarded by a unit test
	case nil:
		return false, nil
	case ErrGone:
		return true, nil
	default:
		return false, err
	}
}

func (p *PIDFD) readEnvBlock() (_ []byte, err error) {
	const environ = "environ"

	path := filepath.Join(p.procFile.Name(), environ)

	// Using openat here so that the file gets opened through the already opened
	// process descriptor instead of going through a filesystem lookup, which
	// might yield another process's env under certain circumstances.
	envFD, err := syscall.Openat(int(p.procFile.Fd()), environ, os.O_RDONLY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, &os.PathError{Op: "openat", Path: path, Err: err}
	}

	envFile := os.NewFile(uintptr(envFD), path)
	defer func() { err = errors.Join(err, envFile.Close()) }()
	return io.ReadAll(envFile)
}

// Obtain a file descriptor that refers to a process (a.k.a a pidfd) via the
// proc(5) filesystem.
//
// Opens a /proc/<pid> directory. The file descriptor obtained in this way is
// not pollable and can't be waited on with waitid(2).
func procfsPidfdOpen(pid PID) (*os.File, error) {

	// Since Linux 5.3, the pidfd_open syscall would be the preferred way of
	// obtaining process file descriptors. They allow for polling and awaiting
	// process termination, which the procfs based file descriptors don't. The
	// problem with those is that there's no way of inspecting the process
	// environment through those. There's some basic memory management support,
	// intended to be used by OOM killers, and the commit message mentions that
	// there might be more interfaces in the future. But for now, the only thing
	// that allows for inspecting the process environment through pidfds is
	// procfs.
	//
	// https://man7.org/linux/man-pages/man2/pidfd_open.2.html
	// https://github.com/torvalds/linux/commit/32fcb426ec001cb6d5a4a195091a8486ea77e2df
	// https://github.com/torvalds/linux/commit/7615d9e1780e26e0178c93c55b73309a5dc093d7

	if err := ensureProcfs(); err != nil {
		return nil, err
	}

	pidDir := filepath.Join(procMount, pid.String())
	return os.OpenFile(pidDir, syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
}

func ensureProcfs() error {
	var st syscall.Statfs_t
	if err := syscall.Statfs(procMount, &st); err != nil {
		if os.IsNotExist(err) {
			return &noProcfsError{err}
		}

		return fmt.Errorf("proc(5) filesystem check failed: failed to statfs %s: %w", procMount, err)
	}

	if st.Type != unix.PROC_SUPER_MAGIC {
		return &noProcfsError{fmt.Errorf("%s: unexpected filesystem type: %x", procMount, st.Type)}
	}

	return nil
}

type noProcfsError struct{ err error }

func (e *noProcfsError) Error() string {
	return fmt.Sprintf("proc(5) filesystem unavailable: %v", e.err)
}

func (e *noProcfsError) Unwrap() error {
	return e.err
}

func (e *noProcfsError) Is(target error) bool {
	// This is kinda like an unsupported syscall.
	if target == syscall.ENOSYS {
		return true
	}

	return errors.Is(e.err, target)
}

// Send a signal to a process specified by a file descriptor.
//
// The calling process must either be in the same PID namespace as the process
// referred to by pidfd, or be in an ancestor of that namespace.
//
// Since Linux 5.1.
// https://man7.org/linux/man-pages/man2/pidfd_send_signal.2.html
// https://github.com/torvalds/linux/commit/3eb39f47934f9d5a3027fe00d906a45fe3a15fad
func pidfdSendSignal(pidfd int, sig syscall.Signal) error {
	// If the info argument is a NULL pointer, this is equivalent to specifying
	// a pointer to a siginfo_t buffer whose fields match the values that are
	// implicitly supplied when a signal is sent using kill(2):
	//
	//   * si_signo is set to the signal number;
	//   * si_errno is set to 0;
	//   * si_code is set to SI_USER;
	//   * si_pid is set to the caller's PID; and
	//   * si_uid is set to the caller's real user ID.
	info := (*unix.Siginfo)(nil)

	// The flags argument is reserved for future use; currently, this
	// argument must be specified as 0.
	flags := 0

	return unix.PidfdSendSignal(pidfd, sig, info, flags)
}
