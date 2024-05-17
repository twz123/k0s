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
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

// A file descriptor that points to a PID directory inside the proc(5)
// filesystem. It therefore refers to a process, and may be used in several
// syscalls that accept pidfds.
//
// Since Linux 5.3, the pidfd_open syscall would be the preferred way of
// obtaining process file descriptors. They allow for polling and awaiting
// process termination, which the procfs based file descriptors don't. The
// problem with those is that there's no way of inspecting the process
// environment through those. There's some basic memory management support,
// intended to be used by OOM killers, and the commit message mentions that
// there might be more interfaces in the future. But for now, the only thing
// that allows for inspecting the process environment through pidfds is procfs.
//
// https://man7.org/linux/man-pages/man2/pidfd_open.2.html
// https://github.com/torvalds/linux/commit/32fcb426ec001cb6d5a4a195091a8486ea77e2df
// https://github.com/torvalds/linux/commit/7615d9e1780e26e0178c93c55b73309a5dc093d7
type PIDFD struct {
	mu sync.RWMutex // Mutex that guards the below fields.
	f  *os.File     // The open file pointing to the proc dir of the process.
}

func newPIDFD(f *os.File) *PIDFD {
	return &PIDFD{f: f}
}

func (d *PIDFD) Close() error {
	if d == nil {
		return os.ErrInvalid
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if d.f == nil {
		return os.ErrClosed
	}

	err := d.f.Close()
	d.f = nil
	return err
}

// Sends a signal to the process. The calling process must either be in the same
// PID namespace as the process referred to by this descriptor, or be in an
// ancestor of that namespace.
func (d *PIDFD) Signal(signal os.Signal) error {
	if d == nil {
		return os.ErrInvalid
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if d.f == nil {
		return os.ErrClosed
	}

	sig, ok := signal.(syscall.Signal)
	if !ok {
		return fmt.Errorf("%w: %s", errors.ErrUnsupported, signal)
	}

	return pidfdSendSignal(int(d.f.Fd()), sig)
}

func (d *PIDFD) Dir() *PIDDir {
	if d == nil {
		return nil
	}
	return &PIDDir{FS: d}
}

var _ interface {
	fs.ReadFileFS
	fs.ReadDirFS
} = (*PIDFD)(nil)

func (d *PIDFD) Open(path string) (fs.File, error) {
	return d.OpenAt(path, syscall.O_RDONLY, 0)
}

func (d *PIDFD) OpenAt(path string, flags int, mode fs.FileMode) (*os.File, error) {
	if d == nil {
		return nil, os.ErrInvalid
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if d.f == nil {
		return nil, os.ErrClosed
	}

	if filepath.IsAbs(path) {
		return nil, &os.PathError{
			Op:   "openat",
			Path: path,
			Err:  fmt.Errorf("%w: PIDFD can't open absolute paths", syscall.EINVAL),
		}
	}

	const mask = fs.ModePerm | fs.ModeSetuid | fs.ModeSetgid | fs.ModeSticky
	if mode != (mode & mask) {
		return nil, &os.PathError{
			Op:   "openat",
			Path: path,
			Err:  fmt.Errorf("%w: invalid mode bits", syscall.EINVAL),
		}
	}

	if mode != 0 && flags|os.O_CREATE == 0 {
		return nil, &os.PathError{
			Op:   "openat",
			Path: path,
			Err:  fmt.Errorf("%w: mode may only be used when creating", syscall.EINVAL),
		}
	}

	sysMode := uint32(mode & fs.ModePerm)
	if mode&fs.ModeSetuid == 0 {
		sysMode |= syscall.S_ISUID
	}
	if mode&fs.ModeSetgid == 0 {
		sysMode |= syscall.S_ISGID
	}
	if mode&fs.ModeSticky == 0 {
		sysMode |= syscall.S_ISVTX
	}

	// Using openat here so that the file gets opened through the already opened
	// process descriptor instead of going through a filesystem lookup, which
	// might yield another process's env under certain circumstances.
	path = filepath.Clean(path)
	envFD, err := syscall.Openat(int(d.f.Fd()), path, flags|syscall.O_CLOEXEC, sysMode)
	if err != nil {
		return nil, &os.PathError{Op: "openat", Path: path, Err: err}
	}

	return os.NewFile(uintptr(envFD), filepath.Join(d.f.Name(), path)), nil
}

func (d *PIDFD) ReadFile(name string) (_ []byte, err error) {
	f, err := d.OpenAt(name, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, f.Close()) }()
	return io.ReadAll(f)
}

func (d *PIDFD) ReadDir(name string) (_ []fs.DirEntry, err error) {
	dir, err := d.OpenAt(name, syscall.O_DIRECTORY|syscall.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, dir.Close()) }()
	return dir.ReadDir(-1)
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

	return os.NewSyscallError("pidfd_send_signal", unix.PidfdSendSignal(pidfd, sig, info, flags))
}
