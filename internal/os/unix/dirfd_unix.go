//go:build unix

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
	"errors"
	"io"
	"iter"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// A file descriptor pointing to a directory (a.k.a. dirfd). It uses the
// syscalls that accept a dirfd, i.e. openat, fstatat ...
//
// Using a dirfd, as opposed to using a path (or path prefix) for all
// operations, offers some unique features: Operations are more consistent. A
// dirfd ensures that all operations are relative to the same directory
// instance. If the directory is renamed or moved, the dirfd remains valid and
// operations continue to work as expected, which is not the case when using
// paths. Using a dirfd can also be more secure. If a directory path is given as
// a string and used repeatedly, there's a risk that the path could be
// maliciously altered (e.g., through symbolic link attacks). Using a dirfd
// ensures that operations use the original directory, mitigating this type of
// attack.
type DirFD os.File

// Opens a [DirFD] referring to the given path.
//
// Note that this is not a chroot: The *at syscalls will only use dirfd to
// resolve relative paths, and will happily follow symlinks and cross mount
// points.
func OpenDir(path string, flags int) (*DirFD, error) {
	// Use the raw syscall instead of os.OpenFile here, as the latter tries to
	// put the fds into non-blocking mode.
	fd, err := syscall.Open(path, flags|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}

	return (*DirFD)(os.NewFile(uintptr(fd), path)), nil
}

// Delegates to [os.File.Close].
func (d *DirFD) Close() error { return (*os.File)(d).Close() }

// Delegates to [os.File.SyscallConn].
func (d *DirFD) SyscallConn() (syscall.RawConn, error) { return (*os.File)(d).SyscallConn() }

// Opens the directory with the given name.
// The name is opened relative to the receiver, using the openat syscall.
//
// https://www.man7.org/linux/man-pages/man2/open.2.html
func (d *DirFD) OpenDir(name string, flags int) (*DirFD, error) {
	var opened int
	err := syscallControl(d, func(fd uintptr) (err error) {
		opened, err = unix.Openat(int(fd), name, flags|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
		if err != nil {
			return &os.PathError{Op: "openat", Path: name, Err: err}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return (*DirFD)(os.NewFile(uintptr(opened), name)), nil
}

// Remove the name and possibly the file it refers to.
// The name is removed relative to the receiver, using the unlinkat syscall.
//
// https://www.man7.org/linux/man-pages/man2/unlink.2.html
func (d *DirFD) Remove(name string) error {
	return d.unlink(name, 0)
}

// Remove the directory with the given name using the unlinkat syscall.
// The name is removed relative to the receiver, using the unlinkat syscall.
//
// https://www.man7.org/linux/man-pages/man2/unlink.2.html
func (d *DirFD) RemoveDir(name string) error {
	return d.unlink(name, unix.AT_REMOVEDIR)
}

func (d *DirFD) unlink(name string, flags int) error {
	return syscallControl(d, func(fd uintptr) error {
		err := unix.Unlinkat(int(fd), name, flags)
		if err != nil {
			return &os.PathError{Op: "unlinkat", Path: name, Err: err}
		}

		return nil
	})
}

// Delegates to [os.File.Readdirnames].
//
// This is the only "safe" option. Both [os.File.ReadDir] and [os.File.Readdir]
// will NOT work because of the way the standard library handles directory
// entries: Both methods may end up using the lstat syscall to stat the
// directory entry pathnames under certain circumstances, which violates the
// assumptions of DirFD, and at best will produce runtime errors or return false
// data, or worse. Possible workarounds would be either to use
// [os.File.Readdirnames] internally and do an fstatat syscall for each of the
// returned pathnames (with a significant performance penalty), or to
// reimplement substantial OS-dependent parts of the standard library's internal
// dir entry handling (which feels like the "nuclear option"). For this reason,
// DirFD cannot simply implement [fs.FS], since the stat-like information should
// also be queryable in the [fs.DirEntry] interface.
func (d *DirFD) Readdirnames(n int) ([]string, error) {
	return (*os.File)(d).Readdirnames(n)
}

// Iterates over all the directory entries, returning their names, in no
// particular order.
func (d *DirFD) ReadEntryNames() iter.Seq2[string, error] {
	return func(yield func(string, error) bool) {
		for {
			// Using n=1 is required in order to be able
			// to resume iteration after early breaks.
			names, err := d.Readdirnames(1)
			var eof bool
			if err != nil {
				if !errors.Is(err, io.EOF) {
					yield("", err)
					return
				}
				eof = true
			}

			for _, name := range names {
				if !yield(name, nil) {
					return
				}
			}

			if eof {
				return
			}
		}
	}
}

func syscallControl(d *DirFD, f func(fd uintptr) error) error {
	rawConn, err := d.SyscallConn()
	if err != nil {
		return err
	}

	outerErr := rawConn.Control(func(fd uintptr) { err = f(fd) })
	if outerErr != nil {
		return outerErr
	}
	return err
}
