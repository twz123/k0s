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
	"context"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/k0sproject/k0s/internal/pkg/dir"
	"github.com/k0sproject/k0s/pkg/constant"
)

// Supervisor is dead simple and stupid process supervisor, just tries to keep the process running in a while-true loop
type Supervisor struct {
	Name           string
	BinPath        string
	RunDir         string
	DataDir        string
	Args           []string
	PidFile        string
	UID            int
	GID            int
	TimeoutStop    time.Duration
	TimeoutRespawn time.Duration
	// For those components having env prefix convention such as ETCD_xxx, we should keep the prefix.
	KeepEnvPrefix bool

	cmd            *exec.Cmd
	done           chan bool
	log            *logrus.Entry
	mutex          sync.Mutex
	startStopMutex sync.Mutex
	cancel         context.CancelFunc
}

// processWaitQuit waits for a process to exit or a shut down signal
// returns true if shutdown is requested
func (s *Supervisor) processWaitQuit(ctx context.Context) bool {
	waitresult := make(chan error)
	go func() {
		waitresult <- s.cmd.Wait()
	}()

	pidbuf := []byte(strconv.Itoa(s.cmd.Process.Pid) + "\n")
	err := os.WriteFile(s.PidFile, pidbuf, constant.PidFileMode)
	if err != nil {
		s.log.Warnf("Failed to write file %s: %v", s.PidFile, err)
	}
	defer os.Remove(s.PidFile)

	select {
	case <-ctx.Done():
		for {
			s.log.Infof("Shutting down pid %d", s.cmd.Process.Pid)
			err := s.cmd.Process.Signal(syscall.SIGTERM)
			if err != nil {
				s.log.Warnf("Failed to send SIGTERM to pid %d: %s", s.cmd.Process.Pid, err)
			}
			select {
			case <-time.After(s.TimeoutStop):
				continue
			case <-waitresult:
				return true
			}
		}
	case err := <-waitresult:
		if err != nil {
			s.log.Warn(err)
		} else {
			s.log.Warnf("Process exited with code: %d", s.cmd.ProcessState.ExitCode())
		}
	}
	return false
}

// Supervise Starts supervising the given process
func (s *Supervisor) Supervise() error {
	s.startStopMutex.Lock()
	defer s.startStopMutex.Unlock()
	// check if it is already started
	if s.cancel != nil {
		s.log.Warn("Already started")
		return nil
	}
	s.log = logrus.WithField("component", s.Name)
	s.PidFile = path.Join(s.RunDir, s.Name) + ".pid"
	if err := dir.Init(s.RunDir, constant.RunDirMode); err != nil {
		s.log.Warnf("failed to initialize dir: %v", err)
		return err
	}

	if s.TimeoutStop == 0 {
		s.TimeoutStop = 5 * time.Second
	}
	if s.TimeoutRespawn == 0 {
		s.TimeoutRespawn = 5 * time.Second
	}

	var ctx context.Context
	ctx, s.cancel = context.WithCancel(context.Background())
	started := make(chan error)
	s.done = make(chan bool)

	go func() {
		defer func() {
			close(s.done)
		}()

		s.log.Info("Starting to supervise")
		restarts := 0
		for {
			s.mutex.Lock()
			s.cmd = exec.Command(s.BinPath, s.Args...)
			s.cmd.Dir = s.DataDir
			s.cmd.Env = envForComponent(os.Environ(), s.DataDir, s.Name, s.KeepEnvPrefix)

			// detach from the process group so children don't
			// get signals sent directly to parent.
			s.cmd.SysProcAttr = DetachAttr(s.UID, s.GID)

			s.cmd.Stdout = s.log.Writer()
			s.cmd.Stderr = s.log.Writer()

			err := s.cmd.Start()
			s.mutex.Unlock()
			if err != nil {
				s.log.Warnf("Failed to start: %s", err)
				if restarts == 0 {
					started <- err
					return
				}
			} else {
				if restarts == 0 {
					s.log.Infof("Started successfully, go nuts pid %d", s.cmd.Process.Pid)
					started <- nil
				} else {
					s.log.Infof("Restarted (%d)", restarts)
				}
				restarts++
				if s.processWaitQuit(ctx) {
					return
				}
			}

			// TODO Maybe some backoff thingy would be nice
			s.log.Infof("respawning in %s", s.TimeoutRespawn.String())

			select {
			case <-ctx.Done():
				s.log.Debug("respawn cancelled")
				return
			case <-time.After(s.TimeoutRespawn):
				s.log.Debug("respawning")
			}
		}
	}()
	return <-started
}

// Stop stops the supervised
func (s *Supervisor) Stop() error {
	s.startStopMutex.Lock()
	defer s.startStopMutex.Unlock()
	if s.cancel == nil || s.log == nil {
		s.log.Warn("Not started")
		return nil
	}
	s.log.Debug("Sending stop message")

	s.cancel()
	s.cancel = nil
	s.log.Debug("Waiting for stopping is done")
	if s.done != nil {
		<-s.done
	}
	return nil
}

// Prepare the env for exec:
// - handle component specific env
// - inject k0s embedded bins into path
func envForComponent(env []string, dataDir, component string, keepEnvPrefix bool) []string {
	type entry struct {
		value         string
		fromComponent bool
	}

	componentPrefix := fmt.Sprintf("%s_", strings.ToUpper(component))
	mergedEnv := make(map[string]entry, len(env))

	for _, e := range env {
		name, value, _ := strings.Cut(e, "=")
		normalizedName := strings.TrimPrefix(name, componentPrefix)
		fromComponent := name != normalizedName
		prevEntry := mergedEnv[name] // rely on the zero value for missing env vars

		// Apply some special cases for certain env vars.
		switch normalizedName {
		// Always override proxy env vars. Never emit prefixed versions of those.
		case "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY":
			if prevEntry.fromComponent {
				// The previous entry was already from a prefixed env var.
				// Don't override it.
				break
			}

			mergedEnv[normalizedName] = entry{value, fromComponent}
			continue

		// Prefix PATH with the k0s bindir.
		case "PATH":
			// Leave the component's path unchanged if env prefixes are kept.
			if fromComponent && keepEnvPrefix {
				break
			}

			value = filepath.Join(dataDir, "bin") + string(filepath.ListSeparator) + value
		}

		// Only store the new value if the previous one didn't come from a prefixed env var.
		if !prevEntry.fromComponent {
			if !keepEnvPrefix {
				// Drop the component's prefix from the env var's name.
				name = normalizedName
			}
			mergedEnv[name] = entry{value, fromComponent}
		}
	}

	out := make([]string, 0, len(mergedEnv))
	for name, entry := range mergedEnv {
		out = append(out, name+"="+entry.value)
	}

	return out
}

// GetProcess returns the last started process
func (s *Supervisor) GetProcess() *os.Process {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	return s.cmd.Process
}
