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

package process

import (
	"errors"
	"fmt"
	"os"
)

// A handle to a running process. May be used to inspect the process's
// environment and to send signals to it.
type Handle interface {
	// Closes this handle and releases OS specific resources.
	Close() error

	// Sends a signal to the process.
	Signal(os.Signal) error

	// Reads and returns the process's environment.
	Environ() ([]string, error)

	// Indicates if this process exited or is still running.
	IsDone() (bool, error)
}

func Open(p *os.Process) (Handle, error) {
	if p == nil {
		return nil, ErrGone
	}

	pid := PID(p.Pid)
	if p.Pid != int(p.Pid) {
		return nil, fmt.Errorf("illegal PID: %d", p.Pid)
	}

	return pid.OpenHandle()
}

// Obtains a handle that refers to a process.
// Returns [ErrPIDNotExist] if there's no such process.
func (p PID) OpenHandle() (Handle, error) {
	return openHandle(p)
}

var (
	ErrPIDNotExist = errors.New("process specified by PID does not exist")
	ErrGone        = errors.New("process gone")
)
