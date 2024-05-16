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

package procfs

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"strconv"
)

// A /proc/pid directory. Exposes methods to parse the file contents.
type PIDDir struct{ fs.FS }

// Reads and parses /proc/pid/environ.
func (d *PIDDir) Environ() (env []string, _ error) {
	raw, err := fs.ReadFile(d, "environ")
	if err != nil {
		return nil, err
	}

	for len(raw) > 0 {
		// Each env variable is NUL terminated.
		current, rest, ok := bytes.Cut(raw, []byte{0})
		if !ok {
			return nil, fmt.Errorf("variable not properly terminated: %q", raw)
		}
		env = append(env, string(current))
		raw = rest
	}
	return env, nil
}

// Reads the state field from /proc/pid/stat.
func (d *PIDDir) State() (PIDState, error) {
	raw, err := fs.ReadFile(d, "stat")
	if err != nil {
		return 0, err
	}
	if idx := bytes.LastIndexByte(raw, ')'); idx < 0 {
		return 0, fmt.Errorf("no closing parenthesis: %q", raw)
	} else {
		raw = raw[idx+1:]
	}
	if len(raw) < 3 || raw[0] != ' ' || raw[2] != ' ' {
		return 0, fmt.Errorf("failed to locate state field: %q", raw)
	}
	return PIDState(raw[1]), nil
}

type PIDStatusField string

type PIDStatus map[string]string

var ErrPIDStatusFieldMissing = errors.New("PID status field missing")

// Thread group ID (i.e., Process ID).
func (s PIDStatus) ThreadGroupID() (uint, error) {
	if tgid, ok := s["Tgid"]; ok {
		tgid, err := strconv.ParseUint(tgid, 10, 64)
		return uint(tgid), err
	}
	return 0, ErrPIDStatusFieldMissing
}

// Reads and parses /proc/pid/status.
func (d *PIDDir) Status() (PIDStatus, error) {
	raw, err := fs.ReadFile(d, "status")
	if err != nil {
		return nil, err
	}

	status := make(PIDStatus, 64)
	for len(raw) > 0 {
		line, rest, ok := bytes.Cut(raw, []byte{'\n'})
		if !ok {
			return nil, fmt.Errorf("status file not properly terminated: %q", raw)
		}
		name, val, ok := bytes.Cut(line, []byte{':'})
		if !ok {
			return nil, fmt.Errorf("line without colon: %q", line)
		}
		status[string(name)] = string(bytes.TrimSpace(val))
		raw = rest
	}

	return status, nil
}
