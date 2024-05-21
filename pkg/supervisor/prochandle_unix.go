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
	"syscall"

	"github.com/k0sproject/k0s/internal/os/linux/procfs"
)

type unixPID int

func newProcHandle(pid int) (procHandle, error) {
	return unixPID(pid), nil
}

func (pid unixPID) cmdline() ([]string, error) {
	pidDir := procfs.NewPIDDIR(uint(pid))
	return pidDir.Cmdline()
}

func (pid unixPID) environ() ([]string, error) {
	pidDir := procfs.NewPIDDIR(uint(pid))
	return pidDir.Environ()
}

func (pid unixPID) terminateGracefully() error {
	return syscall.Kill(int(pid), syscall.SIGTERM)
}

func (pid unixPID) terminateForcibly() error {
	return syscall.Kill(int(pid), syscall.SIGKILL)
}
