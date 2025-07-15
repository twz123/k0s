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

package supervisor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strconv"

	"github.com/k0sproject/k0s/internal/os/windows"
)

func ShutdownHelperHook() {
	if len(os.Args) != 3 {
		return
	}

	if os.Args[1] != "__K0S_SUPERVISOR_SHUTDOWN_HELPER" {
		return
	}

	// Parse the process ID from the command line arguments.
	var processID uint32
	if parsed, err := strconv.ParseUint(os.Args[2], 10, 32); err != nil {
		fmt.Fprintln(os.Stderr, "Error: invalid process ID:", err)
		os.Exit(1)
	} else if parsed == 0 {
		fmt.Fprintln(os.Stderr, "Error: process ID may not be zero")
		os.Exit(1)
	} else {
		processID = uint32(parsed)
	}

	if err := SendCtrlBreak(processID); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(2)
	}

	fmt.Fprintln(os.Stderr, "Done.")
	os.Exit(0)
}

func SendCtrlBreak(processID uint32) error {
	// Prevent this process from receiving any control events
	_, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	return attachedToProcessConsole(processID, func() error {
		return windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, processID)
	})
}

func attachedToProcessConsole(processID uint32, f func() error) (err error) {
	// Detach from current console
	if err := windows.FreeConsole(); err != nil {
		return err
	}
	// Re-attach to parent's console later on
	defer func() { err = errors.Join(err, windows.AttachConsole(windows.ATTACH_PARENT_PROCESS)) }()

	// Attach to the target's console
	if err := windows.AttachConsole(processID); err != nil {
		return err
	}
	// Detach from the process console later on
	defer func() { err = errors.Join(err, windows.FreeConsole()) }()

	return f()
}
