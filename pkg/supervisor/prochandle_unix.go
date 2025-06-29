//go:build unix

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

package supervisor

import (
	"errors"
	"io/fs"
	"syscall"

	"github.com/k0sproject/k0s/internal/os/linux/procfs"
)

type unixPID uint

func newProcHandle(pid int) (procHandle, error) {
	return unixPID(pid), nil
}

func (pid unixPID) cmdline() ([]string, error) {
	fd, err := pid.openFD()
	if err != nil {
		return nil, err
	}
	cmdline, err := fd.Dir().Cmdline()
	return cmdline, errors.Join(err, fd.Close())
}

func (pid unixPID) environ() ([]string, error) {
	fd, err := pid.openFD()
	if err != nil {
		return nil, err
	}
	environ, err := fd.Dir().Environ()
	return environ, errors.Join(err, fd.Close())
}

func (pid unixPID) terminateGracefully() error {
	return syscall.Kill(int(pid), syscall.SIGTERM)
}

func (pid unixPID) terminateForcibly() error {
	return syscall.Kill(int(pid), syscall.SIGKILL)
}

func (pid unixPID) openFD() (procfs.PIDFD, error) {
	fd, err := procfs.OpenPID(uint(pid))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, syscall.ESRCH
	}
	return fd, err
}
