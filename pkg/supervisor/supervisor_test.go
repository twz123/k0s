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
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/k0sproject/k0s/internal/testutil"

	"github.com/stretchr/testify/require"
)

type SupervisorTest struct {
	shouldFail bool
	proc       Supervisor
}

func TestSupervisorStart(t *testing.T) {
	var testSupervisors = []*SupervisorTest{
		{
			shouldFail: false,
			proc: Supervisor{
				Name:    "supervisor-test-sleep",
				BinPath: "/bin/sh",
				RunDir:  ".",
				Args:    []string{"-c", "sleep 1s"},
			},
		},
		{
			shouldFail: false,
			proc: Supervisor{
				Name:    "supervisor-test-fail",
				BinPath: "/bin/sh",
				RunDir:  ".",
				Args:    []string{"-c", "false"},
			},
		},
		{
			shouldFail: true,
			proc: Supervisor{
				Name:    "supervisor-test-non-executable",
				BinPath: "/tmp",
				RunDir:  ".",
			},
		},
		{
			shouldFail: true,
			proc: Supervisor{
				Name:    "supervisor-test-rundir-fail",
				BinPath: "/tmp",
				RunDir:  "/bin/sh/foo/bar",
			},
		},
	}

	for _, s := range testSupervisors {
		err := s.proc.Supervise()
		if err != nil && !s.shouldFail {
			t.Errorf("Failed to start %s: %v", s.proc.Name, err)
		} else if err == nil && s.shouldFail {
			t.Errorf("%s should fail but didn't", s.proc.Name)
		}
		err = s.proc.Stop()
		if err != nil {
			t.Errorf("Failed to stop %s: %v", s.proc.Name, err)
		}
	}
}

func TestEnvForComponent(t *testing.T) {
	t.Parallel()

	testEnv := []string{
		// Default values
		"PATH=default-path",
		"HTTPS_PROXY=default-proxy:8888",
		"k1=v1", // this will be overridden
		"k2=v2", // this will remain untouched

		// Overrides for foo
		"FOO_PATH=foo-path",
		"FOO_HTTPS_PROXY=foo-proxy:1080",
		"FOO_k1=foo_v1", // this overrides k1
		"FOO_k3=foo_v3", // this is not in the default values at all
	}

	for _, test := range []struct {
		name          string
		keepEnvPrefix bool
		expected      []string
	}{
		{"keepEnvPrefix", true, []string{
			"PATH=" + // dataDir's bin in front of the default PATH
				"data-dir" + string(filepath.Separator) + "bin" +
				string(filepath.ListSeparator) +
				"default-path",
			"HTTPS_PROXY=foo-proxy:1080", // default proxy is overridden

			// Default values as is
			"k1=v1",
			"k2=v2",

			// foo's values as is, without FOO_HTTPS_PROXY
			"FOO_PATH=foo-path",
			"FOO_k1=foo_v1",
			"FOO_k3=foo_v3",
		}},

		{"dropEnvPrefix", false, []string{
			"PATH=" + // dataDir's bin in front of foo's FOO_PATH
				"data-dir" + string(filepath.Separator) + "bin" +
				string(filepath.ListSeparator) +
				"foo-path",
			"HTTPS_PROXY=foo-proxy:1080", // default proxy is overridden

			// Values are merged together
			"k1=foo_v1",
			"k2=v2",
			"k3=foo_v3",
		}},
	} {
		test := test // so that the func gets its own copy of the var
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			// Clone testEnv first, as Permute will modify it.
			testEnv := append([]string(nil), testEnv...)

			// Try all permutations of testEnv to ensure that sort order doesn't matter.
			testutil.Permute(testEnv, func() bool {
				env := envForComponent(testEnv, "data-dir", "foo", test.keepEnvPrefix)
				require.ElementsMatch(t, test.expected, env, "For test env: %v", testEnv)
				return true
			})
		})
	}
}

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
		BinPath:        "/bin/sh",
		RunDir:         ".",
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

func TestStopWhileRespawn(t *testing.T) {
	falsePath, err := exec.LookPath("false")
	if err != nil {
		t.Errorf("could not find a path for 'false' executable: %s", err)
	}

	s := Supervisor{
		Name:           "supervisor-test-stop-while-respawn",
		BinPath:        falsePath,
		RunDir:         ".",
		Args:           []string{},
		TimeoutRespawn: 1 * time.Second,
	}
	err = s.Supervise()
	if err != nil {
		t.Errorf("Failed to start %s: %v", s.Name, err)
	}

	// wait til the process exits
	process := s.GetProcess()
	for process != nil && process.Signal(syscall.Signal(0)) == nil {
		time.Sleep(10 * time.Millisecond)
	}

	// try stop while waiting for respawn
	err = s.Stop()
	if err != nil {
		t.Errorf("Failed to stop %s: %v", s.Name, err)
	}
}

func TestMultiThread(t *testing.T) {
	s := Supervisor{
		Name:    "supervisor-test-multithread",
		BinPath: "/bin/sh",
		RunDir:  ".",
		Args:    []string{"-c", "sleep 1s"},
	}
	var wg sync.WaitGroup
	_ = s.Supervise()
	for i := 0; i < 255; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = s.Stop()
			_ = s.Supervise()
		}()
	}
	wg.Wait()
	_ = s.Stop()
}
