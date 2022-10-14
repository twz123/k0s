//go:build !windows
// +build !windows

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
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestRespawn(t *testing.T) {
	tmpDir := t.TempDir()
	pingFifoPath := filepath.Join(tmpDir, "pingfifo")
	pongFifoPath := filepath.Join(tmpDir, "pongfifo")

	err := syscall.Mkfifo(pingFifoPath, 0666)
	if err != nil {
		t.Errorf("Failed to create fifo %s: %v", pingFifoPath, err)
	}
	err = syscall.Mkfifo(pongFifoPath, 0666)
	if err != nil {
		t.Errorf("Failed to create fifo %s: %v", pongFifoPath, err)
	}

	s := Supervisor{
		Name:           "supervisor-test-respawn",
		BinPath:        "sh",
		RunDir:         t.TempDir(),
		Args:           []string{"-c", fmt.Sprintf("cat %s && echo pong > %s", pingFifoPath, pongFifoPath)},
		TimeoutRespawn: 1 * time.Millisecond,
	}
	err = s.Supervise()
	if err != nil {
		t.Errorf("Failed to start %s: %v", s.Name, err)
	}

	// wait til process starts up. fifo will block the write til process reads it
	err = os.WriteFile(pingFifoPath, []byte("ping 1"), 0644)
	if err != nil {
		t.Errorf("Failed to write to fifo %s: %v", pingFifoPath, err)
	}

	// save the pid
	process := s.GetProcess()

	// read the pong to unblock the process so it can exit
	_, _ = os.ReadFile(pongFifoPath)

	// wait til the respawned process again reads the ping fifo
	err = os.WriteFile(pingFifoPath, []byte("ping 2"), 0644)
	if err != nil {
		t.Errorf("Failed to write to fifo %s: %v", pingFifoPath, err)
	}

	// test that a new process got re-spawned
	if process.Pid == s.GetProcess().Pid {
		t.Errorf("Respawn failed: %s", s.Name)
	}

	err = s.Stop()
	if err != nil {
		t.Errorf("Failed to stop %s: %v", s.Name, err)
	}
}
