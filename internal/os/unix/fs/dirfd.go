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
	"fmt"
	"io"
	"io/fs"
	"os"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// A file descriptor pointing to a directory (a.k.a. dirfd). It uses the
// syscalls that accept a dirfd, i.e. openat, fstatat ...
//
// Using a dirfd, as opposed to using a path (or path prefix) for all
// operations, offers some unique features: Operations are more atomic and
// consistent. A dirfd ensures that all operations are relative to the same
// directory instance. If the directory is renamed or moved, the dirfd remains
// valid and operations continue to work as expected, which is not the case when
// using paths. Using a dirfd can also be more secure. If a directory path is
// given as a string and used repeatedly, there's a risk that the path could be
// maliciously altered (e.g., through symbolic link attacks). Using a dirfd
// ensures that operations use the original directory, mitigating this type of
// attack.
type DirFD interface {
	io.Closer

	syscall.Conn

	Read() ([]fs.DirEntry, error)
	StatAt(name string) (unix.Stat_t, error)
	OpenAt(name string, flags int, mode fs.FileMode) (*os.File, error)

	fs.StatFS
	fs.ReadDirFS

	// DirFD cannot implement fs.SubFS, as the FS abstraction doesn't
	// support fs implementations that need to be explicitly closed.
}

type dirFD os.File

// Opens a DirFD pointing to the given path.
func OpenDir(path string) (DirFD, error) {
	f, err := os.OpenFile(path, syscall.O_RDONLY|syscall.O_DIRECTORY, 0)
	if err != nil {
		return nil, err
	}

	return (*dirFD)(f), nil
}

func (d *dirFD) Close() error                          { return (*os.File)(d).Close() }
func (d *dirFD) SyscallConn() (syscall.RawConn, error) { return (*os.File)(d).SyscallConn() }
func (d *dirFD) Read() ([]fs.DirEntry, error)          { return (*os.File)(d).ReadDir(-1) }
func (d *dirFD) Open(name string) (fs.File, error)     { return d.OpenAt(name, syscall.O_RDONLY, 0) }

func (d *dirFD) OpenAt(name string, flags int, mode fs.FileMode) (*os.File, error) {
	var opened int
	err := control(d, func(fd uintptr) error {
		if !fs.ValidPath(name) {
			return &os.PathError{
				Op:   "openat",
				Path: name,
				Err:  fmt.Errorf("%w: path", syscall.EINVAL),
			}
		}

		const mask = fs.ModePerm | fs.ModeSetuid | fs.ModeSetgid | fs.ModeSticky
		if mode != (mode & mask) {
			return &os.PathError{
				Op:   "openat",
				Path: name,
				Err:  fmt.Errorf("%w: mode bits", syscall.EINVAL),
			}
		}

		if mode != 0 && flags|os.O_CREATE == 0 {
			return &os.PathError{
				Op:   "openat",
				Path: name,
				Err:  fmt.Errorf("%w: mode may only be used when creating", syscall.EINVAL),
			}
		}

		sysMode := uint32(mode & fs.ModePerm)
		if mode&fs.ModeSetuid != 0 {
			sysMode |= syscall.S_ISUID
		}
		if mode&fs.ModeSetgid != 0 {
			sysMode |= syscall.S_ISGID
		}
		if mode&fs.ModeSticky != 0 {
			sysMode |= syscall.S_ISVTX
		}

		var err error
		opened, err = unix.Openat(int(fd), name, flags|syscall.O_CLOEXEC, sysMode)
		if err != nil {
			return &os.PathError{Op: "openat", Path: name, Err: err}
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return os.NewFile(uintptr(opened), name), nil
}

func (d *dirFD) StatAt(name string) (stat unix.Stat_t, _ error) {
	err := control(d, func(fd uintptr) error {
		if name == "." {
			if err := unix.Fstat(int(fd), &stat); err != nil {
				return &fs.PathError{Op: "fstat", Path: ".", Err: err}
			}

			return nil
		}

		if !fs.ValidPath(name) {
			return &os.PathError{
				Op:   "fstatat",
				Path: name,
				Err:  fmt.Errorf("%w: path", syscall.EINVAL),
			}
		}

		if err := unix.Fstatat(int(fd), name, &stat, 0); err != nil {
			return &fs.PathError{Op: "fstatat", Path: name, Err: err}
		}

		return nil
	})

	return stat, err
}

func (d *dirFD) ReadDir(name string) (_ []fs.DirEntry, err error) {
	if name == "." { // No need to open another file descriptor for ourselves.
		return d.Read()
	}

	dir, err := d.OpenAt(name, syscall.O_DIRECTORY|syscall.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, dir.Close()) }()
	return dir.ReadDir(-1)
}

func (d *dirFD) Stat(name string) (fs.FileInfo, error) {
	if name == "." {
		return (*os.File)(d).Stat()
	}

	stat, err := d.StatAt(name)
	if err != nil {
		return nil, err
	}

	return &Stat{name, stat}, nil
}

type Stat struct {
	StatName string
	Stat     unix.Stat_t
}

func (s *Stat) Name() string       { return s.StatName }
func (s *Stat) Size() int64        { return s.Stat.Size }
func (s *Stat) IsDir() bool        { return s.Stat.Mode&syscall.S_IFMT == syscall.S_IFDIR }
func (s *Stat) ModTime() time.Time { return time.Unix(s.Stat.Mtim.Unix()) }
func (s *Stat) Sys() any           { return &s.Stat }

func (s *Stat) Mode() fs.FileMode {
	mode := fs.FileMode(s.Stat.Mode) & fs.ModePerm

	// https://www.man7.org/linux/man-pages/man2/fstatat.2.html#EXAMPLES

	switch s.Stat.Mode & syscall.S_IFMT {
	case syscall.S_IFREG: // regular file
		mode |= fs.ModeDevice
	case syscall.S_IFDIR: // directory
		mode |= fs.ModeDir
	case syscall.S_IFIFO: // FIFO/pipe
		mode |= fs.ModeNamedPipe
	case syscall.S_IFLNK: // symlink
		mode |= fs.ModeSymlink
	case syscall.S_IFSOCK: // socket
		mode |= fs.ModeSocket
	case syscall.S_IFCHR: // character device
		mode |= fs.ModeCharDevice
		fallthrough
	case syscall.S_IFBLK: // block device
	default: // unknown?
		mode |= fs.ModeIrregular
	}

	if s.Stat.Mode&syscall.S_ISGID != 0 {
		mode |= fs.ModeSetgid
	}
	if s.Stat.Mode&syscall.S_ISUID != 0 {
		mode |= fs.ModeSetuid
	}
	if s.Stat.Mode&syscall.S_ISVTX != 0 {
		mode |= fs.ModeSticky
	}

	return mode
}

func control(conn syscall.Conn, f func(fd uintptr) error) error {
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
