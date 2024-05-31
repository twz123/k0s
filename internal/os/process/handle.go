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
	"os"
)

// A handle to a running process. May be used to inspect the process's
// environment and to send signals to it.
type Handle interface {
	// Reads and returns the process's environment.
	Environ() ([]string, error)

	// Sends a signal to the process.
	Signal(os.Signal) error

	// Kills the process.
	Kill() error

	// Indicates if the process terminated.
	IsTerminated() (bool, error)
}

// Obtains a handle that refers to a process.
// Returns [ErrNotExist] if there's no such process.
func OpenPID(pid uint) (Handle, error) {
	return openPID(pid)
}

var (
	ErrNotExist   = errors.New("process specified by PID does not exist")
	ErrTerminated = errors.New("process terminated")
)
