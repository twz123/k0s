//go:build windows

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
	"fmt"
	"syscall"

	"golang.org/x/sys/windows"
)

// DetachAttr creates the proper syscall attributes to run the managed processes
// on windows it doesn't use any arguments but just to keep signature similar
func DetachAttr(int, int) *syscall.SysProcAttr {
	return &syscall.SysProcAttr{}
}

var (
	dllKernel32                  = windows.NewLazySystemDLL("kernel32.dll")
	procAttachConsole            = dllKernel32.NewProc("AttachConsole")
	procFreeConsole              = dllKernel32.NewProc("FreeConsole")
	procSetConsoleCtrlHandler    = dllKernel32.NewProc("SetConsoleCtrlHandler")
	procGenerateConsoleCtrlEvent = dllKernel32.NewProc("GenerateConsoleCtrlEvent")
)

func AttachConsole(pid uint32) (err error) {
	if r1, _, err := procAttachConsole.Call(uintptr(pid)); r1 == 0 {
		return fmt.Errorf("AttachConsole syscall failed: %w", err)
	}
	return nil
}
func FreeConsole() (err error) {
	if r1, _, err := procFreeConsole.Call(); r1 == 0 {
		return fmt.Errorf("FreeConsole syscall failed: %w", err)
	}
	return nil
}

func IgnoreCTRLCEvents(ignore bool) (err error) {
	var add uint
	if ignore {
		add = 1
	}
	if r1, _, err := procSetConsoleCtrlHandler.Call(0, uintptr(add)); r1 == 0 {
		return fmt.Errorf("SetConsoleCtrlHandler syscall failed: %w", err)
	}
	return nil
}

// https://learn.microsoft.com/en-us/windows/console/generateconsolectrlevent#parameters
type ConsoleCTRLEvent uint32

// https://learn.microsoft.com/en-us/windows/console/ctrl-c-and-ctrl-break-signals
// https://learn.microsoft.com/en-us/windows/console/generateconsolectrlevent#parameters
var (
	// Generates a CTRL+C signal. This signal cannot be limited to a specific
	// process group. If dwProcessGroupId is nonzero, this function will
	// succeed, but the CTRL+C signal will not be received by processes within
	// the specified process group.
	ConsoleCTRLC ConsoleCTRLEvent = 0
	// Generates a CTRL+BREAK signal.
	ConsoleCTRLBREAK ConsoleCTRLEvent = 1
)

func (e ConsoleCTRLEvent) String() string {
	switch e {
	case ConsoleCTRLC:
		return "CTRL+C"
	case ConsoleCTRLBREAK:
		return "CTRL+BREAK"
	default:
		return fmt.Sprintf("ConsoleCTRLEvent(%d)", uint32(e))
	}
}

// https://learn.microsoft.com/en-us/windows/console/generateconsolectrlevent
func GenerateConsoleCtrlEvent(processGroupId uint32, event ConsoleCTRLEvent) (err error) {
	if r1, _, err := procGenerateConsoleCtrlEvent.Call(uintptr(event), uintptr(processGroupId)); r1 == 0 {
		return fmt.Errorf("GenerateConsoleCtrlEvent syscall failed: %w", err)
	}
	return nil
}
