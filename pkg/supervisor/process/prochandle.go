//go:build windows

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

package process

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"os"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/text/encoding/unicode"
)

// A wrapper around a Windows process handle.
type ProcHandle struct {
	mu     sync.Mutex
	handle windows.Handle
}

func OpenProcHandle(pid PID) (_ *ProcHandle, err error) {
	const ACCESS_FLAGS = 0 |
		windows.PROCESS_QUERY_INFORMATION | // for NtQueryInformationProcess
		windows.PROCESS_VM_READ | // for ReadProcessMemory
		windows.PROCESS_TERMINATE // for TerminateProcess

	sysPID := uint32(pid)
	if pid != PID(sysPID) { // check for lossless conversion
		return nil, fmt.Errorf("invalid PID: %s", pid)
	}

	handle, err := windows.OpenProcess(ACCESS_FLAGS, false, sysPID)
	if err != nil {
		// If there's no such process for the given PID, OpenProcess will return
		// an invalid parameter error. Normalize this to ErrPIDNotExist.
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return nil, ErrPIDNotExist
		}

		return nil, os.NewSyscallError("OpenProcess", err)
	}

	return &ProcHandle{handle: handle}, nil
}

// Close implements [Handle].
func (h *ProcHandle) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()

	handle := h.handle
	h.handle = windows.InvalidHandle

	if handle == windows.InvalidHandle {
		return windows.ERROR_INVALID_HANDLE
	}

	err := windows.CloseHandle(handle)
	if err != nil {
		return os.NewSyscallError("CloseHandle", err)
	}

	return nil
}

// Signal implements [Handle].
func (h *ProcHandle) Signal(signal os.Signal) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.handle == windows.InvalidHandle {
		return windows.ERROR_INVALID_HANDLE
	}

	if signal != os.Kill {
		return fmt.Errorf("%w: %s", syscall.EWINDOWS, signal)
	}

	const exitCode = 1
	err := windows.TerminateProcess(h.handle, exitCode)
	if err != nil {
		err = os.NewSyscallError("TerminateProcess", err)

		// If the process exited in the meantime, TerminateProcess will return
		// an access denied error. Normalize this to ErrGone.
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			exited, exitedErr := h.exited()
			if exitedErr != nil {
				return errors.Join(err, exitedErr)
			}
			if exited {
				return ErrGone
			}
		}

		return err
	}

	return nil
}

// Environ implements [Handle].
func (h *ProcHandle) Environ() ([]string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.handle == windows.InvalidHandle {
		return nil, windows.ERROR_INVALID_HANDLE
	}

	// If there's some WOW64 emulation going on, there's probably different
	// character encodings and other shenanigans involved. This code has not
	// been tested with such processes, so let's be conservative about that.
	if err := ensureNoWOW64Process(h.handle); err != nil {
		return nil, err
	}

	envBlock, err := h.readEnvBlock()
	if err != nil {
		// If the process exited in the meantime, readEnvBlock might return a
		// partial copy error. Normalize this to ErrGone.
		if errors.Is(err, windows.ERROR_PARTIAL_COPY) {
			exited, exitedErr := h.exited()
			if exitedErr != nil {
				return nil, errors.Join(err, exitedErr)
			}
			if exited {
				return nil, ErrGone
			}
		}

		return nil, err
	}

	// The environment block uses Windows wide characters, i.e. UTF-16LE.
	// Convert this into Golang-compatible UTF-8.
	envBlock, err = unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM).NewDecoder().Bytes(envBlock)
	if err != nil {
		return nil, err
	}

	var env []string
	for {
		// The environment variables are separated by NUL characters.
		current, rest, ok := bytes.Cut(envBlock, []byte{0})
		if !ok {
			return nil, fmt.Errorf("variable not properly terminated: %q", envBlock)
		}
		env = append(env, string(current))

		switch len(rest) {
		default:
			envBlock = rest

		case 1: // The whole block is terminated by a NUL character as well.
			if rest[0] == 0 {
				return env, nil
			}
			fallthrough
		case 0:
			return nil, fmt.Errorf("block not properly terminated: %q", rest)
		}
	}
}

// IsDone implements [Handle].
func (h *ProcHandle) IsDone() (bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.handle == windows.InvalidHandle {
		return false, windows.ERROR_INVALID_HANDLE
	}

	return h.exited()
}

func (h *ProcHandle) exited() (bool, error) {
	// If the process exited, the exit code won't be STILL_ACTIVE (a.k.a STATUS_PENDING).
	// https://learn.microsoft.com/en-us/windows/win32/api/processthreadsapi/nf-processthreadsapi-getexitcodeprocess#remarks

	var exitCode uint32
	err := windows.GetExitCodeProcess(h.handle, &exitCode)
	if err != nil {
		return false, os.NewSyscallError("GetExitCodeProcess", err)
	}

	return exitCode != uint32(windows.STATUS_PENDING), nil
}

// Reads the process's environment block.
//
// The format is described here:
// https://learn.microsoft.com/en-us/windows/win32/api/processenv/nf-processenv-getenvironmentstringsw#remarks
func (h *ProcHandle) readEnvBlock() ([]byte, error) {
	info, err := h.queryInformation()
	if err != nil {
		return nil, err
	}

	// Short-circuit if the process terminated in the meantime.
	// See (*ProcHandle).exited for details.
	if info.ExitStatus != windows.STATUS_PENDING {
		return nil, ErrGone
	}

	var peb windows.PEB // https://en.wikipedia.org/wiki/Process_Environment_Block
	err = h.readMemory(unsafe.Pointer(info.PebBaseAddress), (*byte)(unsafe.Pointer(&peb)), unsafe.Sizeof(peb))
	if err != nil {
		return nil, err
	}

	var params windows.RTL_USER_PROCESS_PARAMETERS
	err = h.readMemory(unsafe.Pointer(peb.ProcessParameters), (*byte)(unsafe.Pointer(&params)), unsafe.Sizeof(params))
	if err != nil {
		return nil, err
	}

	if params.EnvironmentSize == 0 {
		return nil, nil
	}

	envBlock := make([]byte, params.EnvironmentSize)
	err = h.readMemory(params.Environment, (*byte)(unsafe.Pointer(&envBlock[0])), uintptr(len(envBlock)))
	if err != nil {
		return nil, err
	}

	return envBlock, nil
}

func (h *ProcHandle) queryInformation() (*windows.PROCESS_BASIC_INFORMATION, error) {
	var data windows.PROCESS_BASIC_INFORMATION
	dataSize := unsafe.Sizeof(data)
	var bytesRead uint32
	err := windows.NtQueryInformationProcess(
		h.handle,
		windows.ProcessBasicInformation,
		unsafe.Pointer(&data),
		uint32(dataSize),
		&bytesRead,
	)
	if err != nil {
		return nil, os.NewSyscallError("NtQueryInformationProcess", err)
	}
	if dataSize != uintptr(bytesRead) {
		return nil, fmt.Errorf("NtQueryInformationProcess: read mismatch (%d != %d)", dataSize, bytesRead)
	}

	return &data, nil
}

func (h *ProcHandle) readMemory(address unsafe.Pointer, buf *byte, size uintptr) error {
	var bytesRead uintptr
	err := windows.ReadProcessMemory(h.handle, uintptr(address), buf, size, &bytesRead)
	if err != nil {
		return os.NewSyscallError("ReadProcessMemory", err)
	}
	if size != bytesRead {
		return fmt.Errorf("ReadProcessMemory: read mismatch (%d != %d)", size, bytesRead)
	}

	return nil
}

func ensureNoWOW64Process(handle windows.Handle) error {
	// https://learn.microsoft.com/en-us/windows/win32/sysinfo/image-file-machine-constants
	const IMAGE_FILE_MACHINE_UNKNOWN = 0 // Unknown

	// On success, returns a pointer to an IMAGE_FILE_MACHINE_* value. The value
	// will be IMAGE_FILE_MACHINE_UNKNOWN if the target process is not a WOW64
	// process; otherwise, it will identify the type of WoW process.
	// https://learn.microsoft.com/en-us/windows/win32/api/wow64apiset/nf-wow64apiset-iswow64process2#parameters
	processMachine := uint16(math.MaxUint16)

	err := windows.IsWow64Process2(handle, &processMachine, nil)
	if err != nil {
		return os.NewSyscallError("IsWow64Process2", err)
	}

	if processMachine == IMAGE_FILE_MACHINE_UNKNOWN {
		return nil
	}

	return fmt.Errorf("%w for WOW64 processes (0x%x)", errors.ErrUnsupported, processMachine)
}
