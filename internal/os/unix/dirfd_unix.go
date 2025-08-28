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
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// An open handle to some path on the file system.
type Path interface {
	io.Closer
	syscall.Conn
	Name() string               // Delegates to [os.File.Name].
	Stat() (os.FileInfo, error) // Delegates to [os.File.Stat].
}

// A file descriptor pointing to a path.
// It is unspecified if that descriptor is referring to a file or a directory.
type PathFD os.File

// Opens a [PathFD] referring to the given path.
//
// This function can be used to open a path without knowing if it's a directory
// or a file, then use [PathFD.Stat] to figure out if [PathFD.UnwrapFile] or
// [PathFD.UnwrapDir] is appropriate.
//
// Note that, in contrast to [os.Open] and [os.OpenFile], the returned
// descriptor is not put into non-blocking mode automatically. Callers may
// decide if they want this by setting the [syscall.O_NONBLOCK] flag.
func OpenPath(path string, flags int, perm os.FileMode) (*PathFD, error) {
	// Use the raw syscall instead of os.OpenFile here, as the latter tries to
	// put the fds into non-blocking mode.
	flags, mode, err := sysOpenFlags(flags, perm)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}

	fd, err := syscall.Open(path, flags, mode)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}

	return (*PathFD)(os.NewFile(uintptr(fd), path)), nil
}

// The interface that [PathFD] is about to implement.
var _ Path = (*PathFD)(nil)

// Delegates to [os.File.Close].
func (p *PathFD) Close() error { return (*os.File)(p).Close() }

// Delegates to [os.File.Name].
func (p *PathFD) Name() string { return (*os.File)(p).Name() }

// Delegates to [os.File.Stat].
func (p *PathFD) Stat() (os.FileInfo, error) { return (*os.File)(p).Stat() }

// Delegates to [os.File.SyscallConn].
func (p *PathFD) SyscallConn() (syscall.RawConn, error) { return (*os.File)(p).SyscallConn() }

// Converts this pointer to an [*os.File] without any additional checks.
//
// Note that both [os.File.ReadDir] and [os.File.Readdir] will NOT work if this
// pointer has been opened via a [DirFD] pointer.
// See [DirFD.Readdirnames] for details.
func (f *PathFD) UnwrapFile() *os.File { return (*os.File)(f) }

// Converts this pointer to a [*DirFD] without any additional checks.
func (f *PathFD) UnwrapDir() *DirFD { return (*DirFD)(f) }

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

// The interface that [DirFD] is about to implement.
var _ Path = (*DirFD)(nil)

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

// Delegates to [os.File.Name].
func (d *DirFD) Name() string { return (*os.File)(d).Name() }

// Delegates to [io.File.Stat].
func (d *DirFD) Stat() (os.FileInfo, error) { return (*os.File)(d).Stat() }

// Opens the path with the given name.
// The path is opened relative to the receiver, using the openat syscall.
//
// Note that, in contrast to [os.Open] and [os.OpenFile], the returned
// descriptor is not put into non-blocking mode automatically. Callers may
// decide if they want this by setting the [unix.O_NONBLOCK] flag.
//
// https://www.man7.org/linux/man-pages/man2/open.2.html
func (d *DirFD) Open(name string, flags int, mode os.FileMode) (*PathFD, error) {
	var opened int
	err := syscallControl(d, func(fd uintptr) error {
		flags, mode, err := sysOpenFlags(flags, mode)
		if err != nil {
			return &os.PathError{Op: "openat", Path: name, Err: err}
		}

		opened, err = unix.Openat(int(fd), name, flags, mode)
		if err != nil {
			return &os.PathError{Op: "openat", Path: name, Err: err}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return (*PathFD)(os.NewFile(uintptr(opened), name)), nil
}

// Stats the path with the given name.
// The name is interpreted relative to the receiver, using the fstatat syscall.
//
// https://www.man7.org/linux/man-pages/man2/stat.2.html
func (d *DirFD) StatAt(name string, flags int) (*FileInfo, error) {
	info := FileInfo{Path: name}
	if err := syscallControl(d, func(fd uintptr) error {
		if err := unix.Fstatat(int(fd), name, (*unix.Stat_t)(&info.Stat), flags); err != nil {
			return &os.PathError{Op: "fstatat", Path: name, Err: err}
		}

		return nil
	}); err != nil {
		return nil, err
	}

	return &info, nil
}

// Reads the named file and returns the contents, similar to [os.ReadFile].
func (d *DirFD) ReadFile(name string) ([]byte, error) {
	// Leverage fs.ReadFile for the heavy lifting.
	// Safety: unsafeDIRFDFS is not working for directories, but for files.
	// Moreover, the fs path normalizations and verifications are not wanted here.
	return fs.ReadFile((*unsafeDIRFDFS)(d), name)
}

type Stat unix.Stat_t

func (s *Stat) ToFileMode() os.FileMode { return toFileMode(s.Mode) }
func (s *Stat) IsDir() bool             { return s.Mode&unix.S_IFMT == unix.S_IFDIR }
func (s *Stat) ModTime() time.Time      { return time.Unix(s.Mtim.Unix()) }
func (s *Stat) Sys() any                { return (*unix.Stat_t)(s) }

type FileInfo struct {
	Path string
	Stat
}

var _ os.FileInfo = (*FileInfo)(nil)

func (i *FileInfo) Name() string      { return filepath.Base(i.Path) }
func (i *FileInfo) Size() int64       { return i.Stat.Size }
func (i *FileInfo) Mode() os.FileMode { return i.ToFileMode() }

func toFileMode[T ~uint16 | ~uint32](unixMode T) os.FileMode {
	fileMode := os.FileMode(unixMode) & os.ModePerm

	// https://www.man7.org/linux/man-pages/man2/fstatat.2.html#EXAMPLES

	switch unixMode & unix.S_IFMT {
	case unix.S_IFREG: // regular file
		// nothing to do
	case unix.S_IFDIR: // directory
		fileMode |= os.ModeDir
	case unix.S_IFIFO: // FIFO/pipe
		fileMode |= os.ModeNamedPipe
	case unix.S_IFLNK: // symlink
		fileMode |= os.ModeSymlink
	case unix.S_IFSOCK: // socket
		fileMode |= os.ModeSocket
	case unix.S_IFCHR: // character device
		fileMode |= os.ModeCharDevice
		fallthrough
	case unix.S_IFBLK: // block device
		fileMode |= os.ModeDevice
	default: // unknown?
		fileMode |= os.ModeIrregular
	}

	if unixMode&unix.S_ISGID != 0 {
		fileMode |= os.ModeSetgid
	}
	if unixMode&unix.S_ISUID != 0 {
		fileMode |= os.ModeSetuid
	}
	if unixMode&unix.S_ISVTX != 0 {
		fileMode |= os.ModeSticky
	}

	return fileMode
}

// This is an internal, unsafe implementation of [fs.FS]. It won't do any input
// path validation, and it's only safe to be used for opening files. Using this
// to open directories is a road to perdition ...
type unsafeDIRFDFS DirFD

// Open implements [fs.FS].
func (u *unsafeDIRFDFS) Open(name string) (fs.File, error) {
	f, err := (*DirFD)(u).Open(name, syscall.O_NONBLOCK, 0)
	return f.UnwrapFile(), err
}

func sysOpenFlags(flags int, mode os.FileMode) (int, uint32, error) {
	const mask = os.ModePerm | os.ModeSetuid | os.ModeSetgid | os.ModeSticky
	if mode != (mode & mask) {
		return 0, 0, errors.New("invalid mode bits")
	}
	if mode != 0 && flags|os.O_CREATE == 0 {
		return 0, 0, errors.New("mode may only be used when creating")
	}

	return flags | syscall.O_CLOEXEC, toSysMode(mode), nil
}

func toSysMode(mode os.FileMode) uint32 {
	sysMode := uint32(mode & os.ModePerm)
	if mode&os.ModeSetuid != 0 {
		sysMode |= syscall.S_ISUID
	}
	if mode&os.ModeSetgid != 0 {
		sysMode |= syscall.S_ISGID
	}
	if mode&os.ModeSticky != 0 {
		sysMode |= syscall.S_ISVTX
	}
	return sysMode
}

func syscallControl[C syscall.Conn](conn C, f func(fd uintptr) error) error {
	rawConn, err := conn.SyscallConn()
	if err != nil {
		return err
	}

	outerErr := rawConn.Control(func(fd uintptr) { err = f(fd) })
	if outerErr != nil {
		return outerErr
	}
	return err
}
