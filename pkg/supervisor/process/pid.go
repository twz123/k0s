/*
Copyright 2023 k0s authors

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

import "strconv"

// An operating system process ID.
type PID uint32

// Parses a PID from a string.
func ParsePID(s string) (PID, error) {
	pid, err := strconv.ParseUint(s, 10, 32)
	if err != nil {
		return 0, err
	}
	return PID(pid), nil
}

// Converts the PID to a string.
func (p PID) String() string {
	return strconv.FormatUint(uint64(p), 10)
}

func (p PID) Open() (Handle, error) {
	return openHandle(p)
}
