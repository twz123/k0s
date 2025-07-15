//go:build windows

/*
Copyright 2025 k0s authors

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

package windows

import (
	"os"

	"golang.org/x/sys/windows"
)

var (
	modKernel32                  = windows.NewLazySystemDLL("kernel32.dll")
	procFreeConsole              = modKernel32.NewProc("FreeConsole")
	procAttachConsole            = modKernel32.NewProc("AttachConsole")
	procGenerateConsoleCtrlEvent = modKernel32.NewProc("GenerateConsoleCtrlEvent")
)

type ConsoleCtrlEvent uint32

// https://learn.microsoft.com/en-us/windows/console/generateconsolectrlevent#parameters
const (
	// Generates a Ctrl+C signal. This signal cannot be limited to a specific
	// process group. If sent to a non-zero process group ID, this function will
	// succeed, but the Ctrl+C signal will not be received by processes within
	// the specified process group.
	CTRL_C_EVENT ConsoleCtrlEvent = 0

	// Generates a Ctrl+Break signal.
	CTRL_BREAK_EVENT ConsoleCtrlEvent = 1
)

// Use the console of the parent of the current process.
//
// https://learn.microsoft.com/en-us/windows/console/generateconsolectrlevent#parameters
const ATTACH_PARENT_PROCESS = ^uint32(0) // -1

// Detaches the calling process from its console.
//
// https://learn.microsoft.com/en-us/windows/console/freeconsole
func FreeConsole() error {
	r, _, err := /* BOOL */ procFreeConsole.Call()
	if r == 0 {
		return os.NewSyscallError("FreeConsole", err)
	}
	return nil
}

// Attaches the calling process to the console of the specified process as a
// client application.
//
// https://learn.microsoft.com/en-us/windows/console/attachconsole
func AttachConsole(processID uint32) error {
	r, _, err := /* BOOL */ procAttachConsole.Call(
		/* _In_ DWORD dwProcessId */ uintptr(processID),
	)
	if r == 0 {
		return os.NewSyscallError("AttachConsole", err)
	}
	return nil
}

// Sends a specified signal to a console process group that shares the console
// associated with the calling process. Causes the control handler functions of
// processes in the target group to be called.
//
// https://learn.microsoft.com/en-us/windows/console/generateconsolectrlevent
func GenerateConsoleCtrlEvent(ctrlEvent ConsoleCtrlEvent, processGroupID uint32) error {
	r, _, err := /* BOOL */ procGenerateConsoleCtrlEvent.Call(
		/* _In_ DWORD dwCtrlEvent      */ uintptr(ctrlEvent),
		/* _In_ DWORD dwProcessGroupId */ uintptr(processGroupID),
	)
	if r == 0 {
		return os.NewSyscallError("GenerateConsoleCtrlEvent", err)
	}
	return nil
}
