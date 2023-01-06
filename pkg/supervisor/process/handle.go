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
	"bytes"
	"errors"
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
	Environ() (map[string]string, error)
}

// Obtains a handle that refers to a process.
// Returns [ErrPIDNotExist] if there's no such process.
func OpenHandle(pid int) (Handle, error) {
	return openHandle(pid)
}

var (
	ErrPIDNotExist = errors.New("process specified by PID does not exist")
	ErrGone        = errors.New("process gone")
)

// Parses a raw environment block into a map of strings.
func parseEnvBlock(envBytes []byte) map[string]string {
	env := make(map[string]string)

	for len(envBytes) > 0 {
		// Each env variable is NUL terminated.
		current, rest, _ := bytes.Cut(envBytes, []byte{0})

		// Each env variable has the form of KEY=VALUE.
		k, v, _ := bytes.Cut(current, []byte{'='})
		env[string(k)] = string(v)

		envBytes = rest
	}

	return env
}
