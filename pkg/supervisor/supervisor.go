/*
Copyright 2020 k0s authors

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
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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
	Stdin          func() io.Reader
	Args           []string
	PidFile        string
	UID            int
	GID            int
	TimeoutStop    time.Duration
	TimeoutRespawn time.Duration
	// For those components having env prefix convention such as ETCD_xxx, we should keep the prefix.
	KeepEnvPrefix bool

	cmd            atomic.Pointer[exec.Cmd]
	done           chan bool
	log            logrus.FieldLogger
	startStopMutex sync.Mutex
	cancel         context.CancelFunc
}

const k0sManaged = "_K0S_MANAGED=yes"

// processWaitQuit waits for a process to exit or a shut down signal
// returns true if shutdown is requested
func (s *Supervisor) processWaitQuit(ctx context.Context, cmd *exec.Cmd) bool {
	waitresult := make(chan error)
	go func() {
		waitresult <- cmd.Wait()
	}()

	defer os.Remove(s.PidFile)

	select {
	case <-ctx.Done():
		for {
			s.log.Info("Requesting graceful shutdown")
			if err := requestGracefulShutdown(cmd.Process); err != nil {
				s.log.WithError(err).Warn("Failed to request graceful shutdown")
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
			s.log.WithError(err).Warn("Failed to wait for process")
		} else {
			s.log.Warnf("Process exited: %s", cmd.ProcessState)
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

	if s.TimeoutStop == 0 {
		s.TimeoutStop = 5 * time.Second
	}
	if s.TimeoutRespawn == 0 {
		s.TimeoutRespawn = 5 * time.Second
	}

	if err := s.maybeKillPidFile(); err != nil {
		if !errors.Is(err, errors.ErrUnsupported) {
			return err
		}

		s.log.WithError(err).Warn("Old process cannot be terminated")
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

		for firstStart := started; ; {
			if cmd, err := s.startProcess(); err != nil {
				if firstStart != nil {
					firstStart <- err
					return
				}

				s.log.WithError(err).Warnf("Failed to start process")
			} else {
				s.cmd.Store(cmd)
				if firstStart != nil {
					close(firstStart)
					firstStart = nil
				}

				if s.processWaitQuit(ctx, cmd) {
					return
				}
			}

			// TODO Maybe some backoff thingy would be nice
			s.log.Infof("Restarting process in %s", s.TimeoutRespawn)

			select {
			case <-ctx.Done():
				s.log.WithError(context.Cause(ctx)).Debug("Exiting supervisor loop")
				return
			case <-time.After(s.TimeoutRespawn):
				s.log.Debug("Restarting process now")
			}
		}
	}()
	return <-started
}

func (s *Supervisor) startProcess() (*exec.Cmd, error) {
	cmd := exec.Command(s.BinPath, s.Args...)
	cmd.Dir = s.DataDir
	cmd.Env = getEnv(s.DataDir, s.Name, s.KeepEnvPrefix)
	if s.Stdin != nil {
		cmd.Stdin = s.Stdin()
	}

	// detach from the process group so children don't
	// get signals sent directly to parent.
	cmd.SysProcAttr = DetachAttr(s.UID, s.GID)

	const maxLogChunkLen = 16 * 1024
	cmd.Stdout = &logWriter{
		log: s.log.WithField("stream", "stdout"),
		buf: make([]byte, maxLogChunkLen),
	}
	cmd.Stderr = &logWriter{
		log: s.log.WithField("stream", "stderr"),
		buf: make([]byte, maxLogChunkLen),
	}

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	s.log.WithField("pid", cmd.Process.Pid).Info("Process started")

	if err := os.WriteFile(s.PidFile, fmt.Appendf(nil, "%d\n", cmd.Process.Pid), constant.PidFileMode); err != nil {
		s.log.Warnf("Failed to write file %s: %v", s.PidFile, err)
	}

	return cmd, nil
}

// Stop stops the supervised
func (s *Supervisor) Stop() {
	s.startStopMutex.Lock()
	defer s.startStopMutex.Unlock()
	if s.cancel == nil || s.log == nil {
		s.log.Warn("Not started")
		return
	}
	s.log.Debug("Sending stop message")

	s.cancel()
	s.cancel = nil
	s.log.Debug("Waiting for stopping is done")
	if s.done != nil {
		<-s.done
	}
}

// maybeKillPidFile checks kills the process in the pidFile if it's has
// the same binary as the supervisor's and also checks that the env
// `_KOS_MANAGED=yes`. This function does not delete the old pidFile as
// this is done by the caller.
func (s *Supervisor) maybeKillPidFile() error {
	pid, err := os.ReadFile(s.PidFile)
	if os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return fmt.Errorf("failed to read PID file %s: %w", s.PidFile, err)
	}

	p, err := strconv.Atoi(strings.TrimSuffix(string(pid), "\n"))
	if err != nil {
		return fmt.Errorf("failed to parse PID file %s: %w", s.PidFile, err)
	}

	ph, err := openPID(p)
	if err != nil {
		return fmt.Errorf("cannot interact with PID %d from PID file %s: %w", p, s.PidFile, err)
	}
	defer ph.Close()

	if err := s.killProcess(ph); err != nil {
		return fmt.Errorf("failed to kill PID %d from PID file %s: %w", p, s.PidFile, err)
	}

	return nil
}

const exitCheckInterval = 200 * time.Millisecond

// Tries to terminate a process gracefully. If it's still running after
// s.TimeoutStop, the process is killed.
func (s *Supervisor) killProcess(ph procHandle) error {
	if shouldKill, err := s.shouldKillProcess(ph); err != nil || !shouldKill {
		return err
	}

	if err := ph.requestGracefulShutdown(); errors.Is(err, syscall.ESRCH) {
		return nil
	} else if err != nil {
		return fmt.Errorf("failed to request graceful termination: %w", err)
	}

	if terminate, err := s.waitForTermination(ph); err != nil || !terminate {
		return err
	}

	if err := ph.kill(); errors.Is(err, syscall.ESRCH) {
		return nil
	} else if err != nil {
		return fmt.Errorf("failed to kill: %w", err)
	}

	return nil
}

func (s *Supervisor) waitForTermination(ph procHandle) (bool, error) {
	deadlineTimer := time.NewTimer(s.TimeoutStop)
	defer deadlineTimer.Stop()
	checkTicker := time.NewTicker(exitCheckInterval)
	defer checkTicker.Stop()

	for {
		select {
		case <-checkTicker.C:
			if shouldKill, err := s.shouldKillProcess(ph); err != nil || !shouldKill {
				return false, nil
			}

		case <-deadlineTimer.C:
			return true, nil
		}
	}
}

func (s *Supervisor) shouldKillProcess(ph procHandle) (bool, error) {
	// only kill process if it has the expected cmd
	if cmd, err := ph.cmdline(); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return false, nil
		}
		return false, err
	} else if len(cmd) > 0 && cmd[0] != s.BinPath {
		return false, nil
	}

	// only kill process if it has the _KOS_MANAGED env set
	if env, err := ph.environ(); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return false, nil
		}
		return false, err
	} else if !slices.Contains(env, k0sManaged) {
		return false, nil
	}

	return true, nil
}

// Prepare the env for exec:
// - handle component specific env
// - inject k0s embedded bins into path
func getEnv(dataDir, component string, keepEnvPrefix bool) []string {
	env := os.Environ()
	componentPrefix := strings.ToUpper(component) + "_"

	// put the component specific env vars in the front.
	sort.Slice(env, func(i, j int) bool { return strings.HasPrefix(env[i], componentPrefix) })

	overrides := map[string]struct{}{}
	i := 0
	for _, e := range env {
		kv := strings.SplitN(e, "=", 2)
		k, v := kv[0], kv[1]
		// if there is already a correspondent component specific env, skip it.
		if _, ok := overrides[k]; ok {
			continue
		}
		if strings.HasPrefix(k, componentPrefix) {
			var shouldOverride bool
			k1 := strings.TrimPrefix(k, componentPrefix)
			switch k1 {
			// always override proxy env
			case "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY":
				shouldOverride = true
			default:
				if !keepEnvPrefix {
					shouldOverride = true
				}
			}
			if shouldOverride {
				k = k1
				overrides[k] = struct{}{}
			}
		}
		switch k {
		case "PATH":
			env[i] = "PATH=" + dir.PathListJoin(path.Join(dataDir, "bin"), v)
		default:
			env[i] = fmt.Sprintf("%s=%s", k, v)
		}
		i++
	}
	env = append([]string{k0sManaged}, env...)
	i++

	return env[:i]
}

// GetProcess returns the last started process
func (s *Supervisor) GetProcess() *os.Process {
	if cmd := s.cmd.Load(); cmd != nil {
		return cmd.Process
	}

	return nil
}
