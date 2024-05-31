//go:build unix

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

package supervisor

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"syscall"
	"time"

	"github.com/k0sproject/k0s/internal/os/linux/procfs"
)

const (
	exitCheckInterval = 200 * time.Millisecond
)

// killPid signals SIGTERM to a PID and if it's still running after
// s.TimeoutStop sends SIGKILL.
func (s *Supervisor) killPid(pid int) error {
	// Kill the process pid
	deadlineTicker := time.NewTicker(s.TimeoutStop)
	defer deadlineTicker.Stop()
	checkTicker := time.NewTicker(exitCheckInterval)
	defer checkTicker.Stop()

Loop:
	for {
		select {
		case <-checkTicker.C:
			shouldKill, err := s.shouldKillProcess(pid)
			if err != nil {
				return err
			}
			if !shouldKill {
				return nil
			}

			err = syscall.Kill(pid, syscall.SIGTERM)
			if errors.Is(err, syscall.ESRCH) {
				return nil
			} else if err != nil {
				return fmt.Errorf("failed to send SIGTERM: %w", err)
			}
		case <-deadlineTicker.C:
			break Loop
		}
	}

	shouldKill, err := s.shouldKillProcess(pid)
	if err != nil {
		return err
	}
	if !shouldKill {
		return nil
	}

	err = syscall.Kill(pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	} else if err != nil {
		return fmt.Errorf("failed to send SIGKILL: %w", err)
	}
	return nil
}

func (s *Supervisor) shouldKillProcess(pid int) (bool, error) {
	pidDir := procfs.NewPIDDIR(uint(pid))
	cmdline, err := pidDir.Cmdline()
	if os.IsNotExist(err) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("failed to read process cmdline: %w", err)
	}

	// only kill process if it has the expected cmd
	if len(cmdline) < 1 || cmdline[0] != s.BinPath {
		return false, nil
	}

	//only kill process if it has the _KOS_MANAGED env set
	env, err := pidDir.Environ()
	if os.IsNotExist(err) {
		return false, nil
	} else if err != nil {
		return false, fmt.Errorf("failed to read process environ: %w", err)
	}

	return slices.Contains(env, k0sManaged), nil
}
