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
	"os"
	"syscall"

	internalwindows "github.com/k0sproject/k0s/internal/os/windows"
	"golang.org/x/sys/windows"
)

var (
	kernel32                     = windows.NewLazySystemDLL("kernel32.dll")
	procGenerateConsoleCtrlEvent = kernel32.NewProc("GenerateConsoleCtrlEvent")
)

type processID uint32

// newProcHandle is not implemented on Windows.
func newProcHandle(pid int) (procHandle, error) {
	return processID(pid), nil
}

// cmdline implements procHandle.
func (p processID) cmdline() (_ []string, err error) {
	return nil, syscall.EWINDOWS
}

// environ implements procHandle.
func (p processID) environ() ([]string, error) {
	h, err := internalwindows.OpenProcHandle(uint32(p))
	if err != nil {
		return nil, err
	}
	env, err := h.Environ()
	return env, errors.Join(err, h.Close())
}

// terminateForcibly implements procHandle.
func (p processID) terminateForcibly() error {
	h, err := internalwindows.OpenProcHandle(uint32(p))
	if err != nil {
		return err
	}
	return errors.Join(h.Kill(), h.Close())
}

// terminateGracefully implements procHandle.
func (p processID) terminateGracefully() error {
	return p.sendCtrlBreak()
}

func (p processID) sendCtrlBreak() error {
	if err := procGenerateConsoleCtrlEvent.Find(); err != nil {
		return err
	}

	r, _, err := procGenerateConsoleCtrlEvent.Call(syscall.CTRL_BREAK_EVENT, uintptr(p))
	if r == 0 {
		if err == nil {
			err = errors.New("FALSE")
		}
		return os.NewSyscallError("GenerateConsoleCtrlEvent", err)
	}

	return nil
}
