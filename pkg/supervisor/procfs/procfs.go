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
	iofs "io/fs"
	"os"
	"path/filepath"
	"strconv"
	"syscall"

	"golang.org/x/sys/unix"
)

type ProcFS string

const (
	DefaultMountPoint        = "/proc"
	Default           ProcFS = DefaultMountPoint
)

func At(mountPoint string) ProcFS {
	return ProcFS(mountPoint)
}

func (fs ProcFS) String() string {
	return string(fs)
}

// Delegates to [Default].
// See [ProcFS.Open].
func OpenPID(pid uint) (*PIDFD, error) {
	return Default.OpenPID(pid)
}

// Obtain a file descriptor that refers to a process (a.k.a a pidfd) via the
// proc(5) filesystem.
//
// Opens a /proc/<pid> directory. The file descriptor obtained in this way is
// not pollable and can't be waited on with waitid(2).
func (fs ProcFS) OpenPID(pid uint) (*PIDFD, error) {
	const flags = syscall.O_DIRECTORY | syscall.O_CLOEXEC
	path := filepath.Join(fs.String(), strconv.FormatUint(uint64(pid), 10))
	pidFile, err := os.OpenFile(path, flags, 0)
	if err == nil {
		return newPIDFD(pidFile), nil
	}

	// If there was an error, check if the procfs is actually valid.
	verifyErr := fs.Verify()
	if verifyErr != nil {
		err = fmt.Errorf("%w (%v)", verifyErr, err) //nolint:errorlint // shadow open err
	}
	return nil, err
}

func (fs ProcFS) Verify() error {
	var st syscall.Statfs_t
	if err := syscall.Statfs(string(fs), &st); err != nil {
		statErr := &iofs.PathError{Op: "statfs", Path: fs.String(), Err: err}
		if errors.Is(err, os.ErrNotExist) {
			err = fmt.Errorf("%w: proc(5) filesystem unavailable", errors.ErrUnsupported)
		} else {
			err = errors.New("proc(5) filesystem check failed")
		}
		return fmt.Errorf("%w: %v", err, statErr) //nolint:errorlint // shadow stat err
	}

	if st.Type != unix.PROC_SUPER_MAGIC {
		return fmt.Errorf("%w: not a proc(5) filesystem: %s: type is 0x%x", errors.ErrUnsupported, fs, st.Type)
	}
	return nil
}
