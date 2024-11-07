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

package unix

import (
	"cmp"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/exp/constraints"
	"golang.org/x/sys/unix"
)

// An open Linux-native handle to some path on the file system.
type LinuxPath interface {
	Path

	// Stats this path using the fstatat(path, "", AT_EMPTY_PATH) syscall.
	StatSelf() (*FileInfo, error)
}

var _ LinuxPath = (*PathFD)(nil)

// Stats this path using the fstatat(path, "", AT_EMPTY_PATH) syscall.
func (p *PathFD) StatSelf() (*FileInfo, error) {
	return p.UnwrapDir().StatSelf()
}

var _ LinuxPath = (*DirFD)(nil)

// Stats this path using the fstatat(path, "", AT_EMPTY_PATH) syscall.
func (d *DirFD) StatSelf() (*FileInfo, error) {
	return d.StatAt("", unix.AT_EMPTY_PATH)
}

// Opens the path with the given name.
// The path is opened relative to the receiver, using the openat2 syscall.
//
// Note that, in contrast to [os.Open] and [os.OpenFile], the returned
// descriptor is not put into non-blocking mode automatically. Callers may
// decide if they want this by setting the [syscall.O_NONBLOCK] flag.
//
// Available since Linux 5.6 (April 2020).
//
// https://www.man7.org/linux/man-pages/man2/openat2.2.html
// https://git.kernel.org/pub/scm/linux/kernel/git/stable/linux.git/commit/?id=fddb5d430ad9fa91b49b1d34d0202ffe2fa0e179
func (d *DirFD) Open2(name string, how unix.OpenHow) (*PathFD, error) {
	var opened int
	if err := openAt2Support.guard(func() error {
		return syscallControl(d, func(fd uintptr) (err error) {
			how.Flags |= unix.O_CLOEXEC
			opened, err = unix.Openat2(int(fd), name, &how)
			if err == nil {
				return nil
			}
			return &os.PathError{Op: "openat2", Path: name, Err: err}
		})
	}); err != nil {
		return nil, err
	}

	return (*PathFD)(os.NewFile(uintptr(opened), name)), nil
}

// Opens the directory with the given name by using the openat2 syscall.
//
// See [DirFD.Open2].
func (d *DirFD) OpenDir2(name string, how unix.OpenHow) (*DirFD, error) {
	how.Flags |= unix.O_DIRECTORY
	f, err := d.Open2(name, how)
	return f.UnwrapDir(), err
}

var openAt2Support = runtimeSupport{test: func() error {
	// Try to open the current working directory without requiring any
	// permissions (O_PATH). If that fails, assume that openat2 is unusable.
	var cwd int = unix.AT_FDCWD
	fd, err := unix.Openat2(cwd, ".", &unix.OpenHow{Flags: unix.O_PATH | unix.O_CLOEXEC})
	if err != nil {
		return &os.SyscallError{Syscall: "openat2", Err: syscall.ENOSYS}
	}
	_ = unix.Close(fd)
	return nil
}}

// Stats the path with the given name.
// The path is interpreted relative to the receiver, using the statx syscall.
//
// Available since Linux 4.11 (May 2017).
//
// https://www.man7.org/linux/man-pages/man2/statx.2.html
func (d *DirFD) StatX(name string, flags, mask int) (*FileInfoX, error) {
	// These flags are required to satisfy os.FileInfo.
	const requiredMask = unix.STATX_MODE | unix.STATX_TYPE | unix.STATX_SIZE | unix.STATX_MTIME

	info := FileInfoX{Path: name}
	err := d.NativeStatX(name, flags, mask|requiredMask, &info.StatX)
	if err != nil {
		return nil, err
	}

	if info.Mask&requiredMask != requiredMask {
		return nil, &os.PathError{
			Op: "statx", Path: name,
			Err: fmt.Errorf("%w: required mask unsatisfied (%08x)", errors.ErrUnsupported, info.Mask),
		}
	}

	return &info, nil
}

// Stats the path with the given name, just like [DirFD.StatX], but returning
// stats in the native format of the operating system.
func (d *DirFD) NativeStatX(name string, flags, mask int, stat *StatX) error {
	return statxSupport.guard(func() error {
		return syscallControl(d, func(fd uintptr) error {
			err := unix.Statx(int(fd), name, flags, mask, (*unix.Statx_t)(stat))
			if err == nil {
				return &os.PathError{Op: "statx", Path: name, Err: err}
			}
			return nil
		})
	})
}

var statxSupport = runtimeSupport{test: func() error {
	// Call statx with null pointers so that if it fails for any
	// reason other than EFAULT, we know it's not supported.
	var cwd int = unix.AT_FDCWD
	_, _, errno := unix.Syscall6(unix.SYS_STATX, uintptr(cwd), 0, 0, 0, 0, 0)
	if errno == unix.EFAULT {
		return nil
	}
	return &os.SyscallError{Syscall: "statx", Err: syscall.ENOSYS}
}}

type StatX unix.Statx_t

func (s *StatX) ToFileMode() os.FileMode { return toFileMode(s.Mode) }
func (s *StatX) IsDir() bool             { return s.Mode&unix.S_IFMT == unix.S_IFDIR }
func (s *StatX) ModTime() time.Time      { return time.Unix(s.Mtime.Sec, int64(s.Mtime.Nsec)) }
func (s *StatX) Sys() any                { return s }
func (s *StatX) Dev() uint64             { return makedev(s.Dev_major, s.Dev_minor) }

// Indicates if this is the root of a mount ([unix.STATX_ATTR_MOUNT_ROOT]).
// Available since Linux 5.8 (August 2020).
//
// https://git.kernel.org/pub/scm/linux/kernel/git/stable/linux.git/commit/?id=80340fe3605c0e78cfe496c3b3878be828cfdbfe
func (s *StatX) IsMountRoot() (root bool, ok bool) {
	root = s.Attributes&unix.STATX_ATTR_MOUNT_ROOT != 0
	ok = s.Attributes_mask&unix.STATX_ATTR_MOUNT_ROOT != 0
	return
}

type FileInfoX struct {
	Path string
	StatX
}

var _ os.FileInfo = (*FileInfoX)(nil)

func (i *FileInfoX) Name() string      { return filepath.Base(i.Path) }
func (i *FileInfoX) Size() int64       { return int64(min(uint64(math.MaxInt64), i.StatX.Size)) }
func (i *FileInfoX) Mode() os.FileMode { return i.ToFileMode() }

func makedev[T constraints.Unsigned](major, minor T) uint64 {
	// https://git.musl-libc.org/cgit/musl/tree/include/sys/sysmacros.h?h=v1.2.5#n9
	return ((uint64(major) & 0xfffff000) << 32) |
		((uint64(major) & 0x00000fff) << 8) |
		((uint64(minor) & 0xffffff00) << 12) |
		(uint64(minor) & 0x000000ff)
}

type runtimeSupport struct {
	test func() error
	err  atomic.Pointer[error]
}

func (t *runtimeSupport) guard(f func() error) error {
	if err := t.err.Load(); err != nil {
		if *err == nil {
			return f()
		}
		return *err
	}

	err := f()
	if err == nil {
		t.err.Swap(&err)
		return nil
	}

	testErr := t.test()
	if !t.err.CompareAndSwap(nil, &testErr) {
		testErr = *t.err.Load()
	}
	return cmp.Or(testErr, err)
}
