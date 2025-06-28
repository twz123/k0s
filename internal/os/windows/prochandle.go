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

package windows

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"os"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/text/encoding/unicode"
)

// A Windows process handle.
type ProcHandle os.File

func OpenProcHandle(processID uint32) (_ *ProcHandle, err error) {
	const ACCESS_FLAGS = 0 |
		windows.PROCESS_QUERY_INFORMATION | // for NtQueryInformationProcess
		windows.PROCESS_VM_READ | // for ReadProcessMemory
		windows.PROCESS_TERMINATE // for TerminateProcess

	handle, err := windows.OpenProcess(ACCESS_FLAGS, false, processID)
	if err != nil {
		// If there's no such process for the given PID, OpenProcess will return
		// an invalid parameter error. Normalize this to syscall.ESRCH.
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return nil, syscall.ESRCH
		}

		return nil, os.NewSyscallError("OpenProcess", err)
	}

	return (*ProcHandle)(os.NewFile(uintptr(handle), "")), nil
}

func (h *ProcHandle) Close() error                          { return (*os.File)(h).Close() }
func (h *ProcHandle) SyscallConn() (syscall.RawConn, error) { return (*os.File)(h).SyscallConn() }

func (h *ProcHandle) Kill() error {
	return control(h, func(fd uintptr) error {
		handle := windows.Handle(fd)

		// Exit code 137 will be returned e.g. by shells when they observe child
		// process termination due to a SIGKILL. Let's simulate this for Windows.
		err := windows.TerminateProcess(handle, 137)
		if err == nil {
			return nil
		}

		err = os.NewSyscallError("TerminateProcess", err)

		// If the process exited in the meantime, TerminateProcess will return
		// an access denied error. Normalize this to syscall.ESRCH.
		if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			exited, exitedErr := exited(handle)
			if exitedErr != nil {
				return errors.Join(err, exitedErr)
			}
			if exited {
				return syscall.ESRCH
			}
		}

		return err
	})
}

func (h *ProcHandle) Environ() (env []string, _ error) {
	var envBlock []byte
	err := control(h, func(fd uintptr) (err error) {
		handle := windows.Handle(fd)

		// If there's some WOW64 emulation going on, there's probably different
		// character encodings and other shenanigans involved. This code has not
		// been tested with such processes, so let's be conservative about that.
		err = ensureNoWOW64Process(handle)
		if err != nil {
			return err
		}

		envBlock, err = readEnvBlock(handle)
		if err == nil {
			return nil
		}

		// If the process exited in the meantime, readEnvBlock might return a
		// partial copy error. Normalize this to syscall.ESRCH.
		if errors.Is(err, windows.ERROR_PARTIAL_COPY) {
			exited, exitedErr := exited(handle)
			if exitedErr != nil {
				return errors.Join(err, exitedErr)
			}
			if exited {
				return syscall.ESRCH
			}
		}

		return err
	})

	// The environment block uses Windows wide characters, i.e. UTF-16LE.
	// Convert this into Golang-compatible UTF-8.
	envBlock, err = unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM).NewDecoder().Bytes(envBlock)
	if err != nil {
		return nil, err
	}

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

func (h *ProcHandle) IsTerminated() (terminated bool, err error) {
	err = control(h, func(fd uintptr) (err error) {
		terminated, err = exited(windows.Handle(fd))
		return err
	})
	return
}

func exited(handle windows.Handle) (bool, error) {
	// If the process exited, the exit code won't be STILL_ACTIVE (a.k.a STATUS_PENDING).
	// https://learn.microsoft.com/en-us/windows/win32/api/processthreadsapi/nf-processthreadsapi-getexitcodeprocess#remarks

	var exitCode uint32
	err := windows.GetExitCodeProcess(handle, &exitCode)
	if err != nil {
		return false, os.NewSyscallError("GetExitCodeProcess", err)
	}

	return exitCode != uint32(windows.STATUS_PENDING), nil
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

// Reads the process's environment block.
//
// The format is described here:
// https://learn.microsoft.com/en-us/windows/win32/api/processenv/nf-processenv-getenvironmentstringsw#remarks
func readEnvBlock(handle windows.Handle) ([]byte, error) {
	info, err := queryInformation(handle)
	if err != nil {
		return nil, err
	}

	// Short-circuit if the process terminated in the meantime.
	// See (*ProcHandle).exited for details.
	if info.ExitStatus != windows.STATUS_PENDING {
		return nil, syscall.ESRCH
	}

	var peb windows.PEB // https://en.wikipedia.org/wiki/Process_Environment_Block
	err = readMemory(handle, unsafe.Pointer(info.PebBaseAddress), (*byte)(unsafe.Pointer(&peb)), unsafe.Sizeof(peb))
	if err != nil {
		return nil, err
	}

	var params windows.RTL_USER_PROCESS_PARAMETERS
	err = readMemory(handle, unsafe.Pointer(peb.ProcessParameters), (*byte)(unsafe.Pointer(&params)), unsafe.Sizeof(params))
	if err != nil {
		return nil, err
	}

	if params.EnvironmentSize == 0 {
		return nil, nil
	}

	envBlock := make([]byte, params.EnvironmentSize)
	err = readMemory(handle, params.Environment, (*byte)(unsafe.Pointer(&envBlock[0])), uintptr(len(envBlock)))
	if err != nil {
		return nil, err
	}

	return envBlock, nil
}

func queryInformation(handle windows.Handle) (*windows.PROCESS_BASIC_INFORMATION, error) {
	var data windows.PROCESS_BASIC_INFORMATION
	dataSize := unsafe.Sizeof(data)
	var bytesRead uint32
	err := windows.NtQueryInformationProcess(
		handle,
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

func readMemory(handle windows.Handle, address unsafe.Pointer, buf *byte, size uintptr) error {
	var bytesRead uintptr
	err := windows.ReadProcessMemory(handle, uintptr(address), buf, size, &bytesRead)
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
