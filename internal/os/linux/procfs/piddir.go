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
	"math/bits"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// A /proc/pid directory.
// Exposes methods to parse and interpret the contents of this directory.
//
// See https://man7.org/linux/man-pages/man5/proc.5.html
type PIDDir struct{ fs.FS }

func idiomaticPIDDirPath(pid uint) string {
	return filepath.Join("/proc", strconv.FormatUint(uint64(pid), 10))
}

// Returns a PIDDir for the given PID, reading contents from their idiomatic
// paths. No checks are performed, so there may be no process with the given
// PID, or the proc filesystem may not be mounted at all.
func NewPIDDIR(pid uint) *PIDDir {
	return &PIDDir{os.DirFS(idiomaticPIDDirPath(pid))}
}

// Reads and parses /proc/pid/cmdline. Returns complete command line for the
// process, if the process is still running and didn't modify that memory region
// in unexpected ways.
func (d *PIDDir) Cmdline() (cmdline []string, _ error) {
	// If the process is a zombie, there is nothing in this file: that is, a
	// read on this file will return 0 characters.
	raw, err := fs.ReadFile(d, "cmdline")
	if err != nil {
		return nil, err
	}

	// The command-line arguments appear in the same layout as they do in
	// process memory: If the process is well-behaved, it is a set of strings
	// separated by null bytes, with a further null byte after the last string.
	// This is the common case, but processes have the freedom to override the
	// memory region and break assumptions about the contents or format of the
	// file contents. Think of the contents as the command line that the process
	// wants you to see.
	for len(raw) > 0 {
		current, rest, ok := bytes.Cut(raw, []byte{0})
		if !ok {
			return nil, fmt.Errorf("cmdline not properly terminated: %q", raw)
		}
		cmdline = append(cmdline, string(current))
		raw = rest
	}
	return cmdline, nil
}

// Reads and parses /proc/pid/environ. Returns the initial environment that was
// set when the currently executing program was started. If the process modifies
// its environment afterwards, Environ will not reflect those changes.
// Permission to access the process environment is governed by a ptrace access
// mode PTRACE_MODE_READ_FSCREDS check.
func (d *PIDDir) Environ() ([]string, error) {
	raw, err := fs.ReadFile(d, "environ")
	if err != nil {
		return nil, err
	}

	// FIXME this returns a trailing empty string.
	// Needs tests!

	// The entries are separated by null bytes,
	// and there may be a null byte at the end.
	return strings.Split(string(raw), "\x00"), nil
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

type PIDStatus map[string]string

// Thread group ID (i.e., Process ID).
func (s PIDStatus) ThreadGroupID() (uint, error) {
	if tgid, ok := s["Tgid"]; ok {
		tgid, err := strconv.ParseUint(tgid, 10, bits.UintSize)
		return uint(tgid), err
	}
	return 0, errors.New("no such field")
}
