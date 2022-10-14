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
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/k0sproject/k0s/internal/testutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type SupervisorTest struct {
	shouldFail bool
	proc       Supervisor
}

func TestSupervisorStart(t *testing.T) {
	tmpDir := t.TempDir()
	var testSupervisors = []*SupervisorTest{
		{
			shouldFail: false,
			proc: Supervisor{
				Name:    "supervisor-test-sleep",
				BinPath: "sh",
				RunDir:  tmpDir,
				Args:    []string{"-c", "sleep 1s"},
			},
		},
		{
			shouldFail: false,
			proc: Supervisor{
				Name:    "supervisor-test-fail",
				BinPath: "sh",
				RunDir:  tmpDir,
				Args:    []string{"-c", "false"},
			},
		},
		{
			shouldFail: true,
			proc: Supervisor{
				Name:    "supervisor-test-non-executable",
				BinPath: tmpDir,
				RunDir:  tmpDir,
			},
		},
		{
			shouldFail: true,
			proc: Supervisor{
				Name:    "supervisor-test-rundir-fail",
				BinPath: tmpDir,
				RunDir:  filepath.Join(tmpDir, "foo", "bar"),
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

func TestGetEnv(t *testing.T) {
	// backup environment vars
	oldEnv := os.Environ()

	os.Clearenv()
	os.Setenv("k3", "v3")
	os.Setenv("PATH", "/bin")
	os.Setenv("k2", "v2")
	os.Setenv("FOO_k3", "foo_v3")
	os.Setenv("k4", "v4")
	os.Setenv("FOO_k2", "foo_v2")
	os.Setenv("FOO_HTTPS_PROXY", "a.b.c:1080")
	os.Setenv("HTTPS_PROXY", "1.2.3.4:8888")
	os.Setenv("k1", "v1")
	os.Setenv("FOO_PATH", "/usr/local/bin")

	env := getEnv("/var/lib/k0s", "foo", false)
	sort.Strings(env)
	expected := "[HTTPS_PROXY=a.b.c:1080 PATH=/var/lib/k0s/bin:/usr/local/bin k1=v1 k2=foo_v2 k3=foo_v3 k4=v4]"
	actual := fmt.Sprintf("%s", env)
	if actual != expected {
		t.Errorf("Failed in env processing with keepEnvPrefix=false, expected: %q, actual: %q", expected, actual)
	}

	env = getEnv("/var/lib/k0s", "foo", true)
	sort.Strings(env)
	expected = "[FOO_PATH=/usr/local/bin FOO_k2=foo_v2 FOO_k3=foo_v3 HTTPS_PROXY=a.b.c:1080 PATH=/var/lib/k0s/bin:/bin k1=v1 k2=v2 k3=v3 k4=v4]"
	actual = fmt.Sprintf("%s", env)
	if actual != expected {
		t.Errorf("Failed in env processing with keepEnvPrefix=true, expected: %q, actual: %q", expected, actual)
	}

	//restore environment vars
	os.Clearenv()
	for _, e := range oldEnv {
		kv := strings.SplitN(e, "=", 2)
		os.Setenv(kv[0], kv[1])
	}
}

func TestRespawnX(t *testing.T) {
	tmpDir := t.TempDir()
	defer testutil.Chdir(t, tmpDir)()

	s := Supervisor{
		Name:           "supervisor-test-respawn",
		BinPath:        "sh",
		RunDir:         t.TempDir(),
		Args:           []string{"-xc", "echo 1 > ping && while [ ! -f pong ]; do sleep 0.02; done && rm ping pong"},
		TimeoutRespawn: 1 * time.Millisecond,
	}

	require.NoError(t, s.Supervise())
	t.Cleanup(func() { assert.NoError(t, s.Stop()) })

	// wait until process starts up
	waitForPing := func() func() error {
		w, err := fsnotify.NewWatcher()
		require.NoError(t, err)
		t.Cleanup(func() { assert.NoError(t, w.Close()) })
		require.NoError(t, w.Add("."))

		var prev os.FileInfo
		return func() error {
			timeout := time.NewTimer(3 * time.Second)
			defer timeout.Stop()

			for {
				select {
				case event := <-w.Events:
					t.Logf("Event: %v", event)
					if filepath.Base(event.Name) == "ping" && event.Has(fsnotify.Write) {
						current, err := os.Stat("ping")
						if err != nil {
							return err
						}
						if prev == nil || current.ModTime().After(prev.ModTime()) {
							prev = current
							return nil
						}
					}
				case err := <-w.Errors:
					return fmt.Errorf("error while watching file system: %w", err)
				case <-timeout.C:
					return errors.New("failed to wait for ping")
				}
			}
		}
	}()
	require.NoError(t, waitForPing())

	// save the pid
	process := s.GetProcess()

	// send the pong to unblock the process so it can exit
	require.NoError(t, os.WriteFile("pong", []byte{}, 0644))

	// wait until the respawned process again touches ping
	require.NoError(t, waitForPing())

	// test that a new process got re-spawned
	assert.NotEqual(t, process.Pid, s.GetProcess().Pid)
}

func TestStopWhileRespawn(t *testing.T) {
	falsePath, err := exec.LookPath("false")
	if err != nil {
		t.Fatalf("could not find a path for 'false' executable: %s", err)
	}

	s := Supervisor{
		Name:           "supervisor-test-stop-while-respawn",
		BinPath:        falsePath,
		RunDir:         t.TempDir(),
		Args:           []string{},
		TimeoutRespawn: 1 * time.Second,
	}
	err = s.Supervise()
	if err != nil {
		t.Fatalf("Failed to start %s: %v", s.Name, err)
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
		BinPath: "sh",
		RunDir:  t.TempDir(),
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
