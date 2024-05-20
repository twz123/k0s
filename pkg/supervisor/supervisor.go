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
	"math"
	"os"
	"os/exec"
	"path"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/k0sproject/k0s/internal/pkg/dir"
	"github.com/k0sproject/k0s/pkg/constant"
	"github.com/k0sproject/k0s/pkg/supervisor/process"
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
	// ProcFSPath is only used for testing
	ProcFSPath string
	// KillFunction is only used for testing
	KillFunction func(int, syscall.Signal) error
	// A function to clean some leftovers before starting or restarting the supervised process
	CleanBeforeFn func() error

	cmd            *exec.Cmd
	done           <-chan struct{}
	log            logrus.FieldLogger
	mutex          sync.Mutex
	startStopMutex sync.Mutex
	cancel         context.CancelFunc
}

const k0sManaged = "_K0S_MANAGED=yes"

// processWaitQuit waits for a process to exit or a shut down signal
// returns true if shutdown is requested
func (s *Supervisor) processWaitQuit(ctx context.Context) bool {
	waitresult := make(chan error)
	go func() {
		waitresult <- s.cmd.Wait()
	}()

	defer os.Remove(s.PidFile)

	select {
	case <-ctx.Done():
		for {
			if runtime.GOOS == "windows" {
				// Graceful shutdown not implemented on Windows. This requires
				// attaching to the target process's console and generating a
				// CTRL+BREAK (or CTRL+C) event. Since a process can only be
				// attached to a single console at a time, this would require
				// k0s to detach from its own console, which is definitely not
				// something that k0s wants to do. There might be ways to do
				// this by generating the event via a separate helper process,
				// but that's left open here as a TODO.
				// https://learn.microsoft.com/en-us/windows/console/freeconsole
				// https://learn.microsoft.com/en-us/windows/console/attachconsole
				// https://learn.microsoft.com/en-us/windows/console/generateconsolectrlevent
				// https://learn.microsoft.com/en-us/windows/console/ctrl-c-and-ctrl-break-signals
				s.log.Infof("Killing pid %d", s.cmd.Process.Pid)
				if err := s.cmd.Process.Kill(); err != nil {
					s.log.Warnf("Failed to kill pid %d: %s", s.cmd.Process.Pid, err)
				}
			} else {
				s.log.Infof("Shutting down pid %d", s.cmd.Process.Pid)
				if err := s.cmd.Process.Signal(syscall.SIGTERM); err != nil {
					s.log.Warnf("Failed to send SIGTERM to pid %d: %s", s.cmd.Process.Pid, err)
				}
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
			s.log.Warnf("Process exited: %s", s.cmd.ProcessState)
		}
	}
	return false
}

// Supervise Starts supervising the given process
func (s *Supervisor) Supervise() error {
	return s.SuperviseC(context.TODO())
}

// Supervise Starts supervising the given process
func (s *Supervisor) SuperviseC(ctx context.Context) error {
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

	// if err := s.clearPIDFile(ctx); err != nil {
	// 	s.log.WithError(err).Error("Failed to clear PID file")
	// }

	if err := s.maybeKillPidFile(ctx, nil, nil); err != nil {
		return err
	}

	superviseCtx, cancel := context.WithCancelCause(context.Background())
	started := make(chan struct{})
	done := make(chan struct{})

	go func() {
		defer func() {
			cancel(errors.New("goroutine exited"))
			close(done)
		}()

		ctx := superviseCtx
		started := (chan<- struct{})(started)
		s.log.Info("Starting to supervise")
		for restarts := 0; ; {
			s.mutex.Lock()

			var err error
			if s.CleanBeforeFn != nil {
				err = s.CleanBeforeFn()
			}
			if err != nil {
				s.log.Warnf("Failed to clean before running the process %s: %s", s.BinPath, err)
			} else {
				s.cmd = exec.Command(s.BinPath, s.Args...)
				s.cmd.Dir = s.DataDir
				s.cmd.Env = getEnv(s.DataDir, s.Name, s.KeepEnvPrefix)

				// detach from the process group so children don't
				// get signals sent directly to parent.
				s.cmd.SysProcAttr = DetachAttr(s.UID, s.GID)

				const maxLogChunkLen = 16 * 1024
				s.cmd.Stdout = &logWriter{
					log: s.log.WithField("stream", "stdout"),
					buf: make([]byte, maxLogChunkLen),
				}
				s.cmd.Stderr = &logWriter{
					log: s.log.WithField("stream", "stderr"),
					buf: make([]byte, maxLogChunkLen),
				}

				err = s.cmd.Start()
			}
			s.mutex.Unlock()
			if err != nil {
				s.log.Warnf("Failed to start: %s", err)
				if started != nil {
					cancel(err)
					return
				}
			} else {
				err := os.WriteFile(s.PidFile, []byte(strconv.Itoa(s.cmd.Process.Pid)+"\n"), constant.PidFileMode)
				if err != nil {
					s.log.WithError(err).Warn("Failed to write PID file for PID ", s.cmd.Process.Pid)
				}

				if started != nil {
					s.log.Infof("Started successfully, go nuts pid %d", s.cmd.Process.Pid)
					close(started)
					started = nil
				} else {
					restarts++
					s.log.Infof("Restarted (%d)", restarts)
				}
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

	select {
	case <-started:
		s.cancel, s.done = func() { cancel(nil) }, done
		return nil

	case <-superviseCtx.Done():
		return context.Cause(superviseCtx)

	case <-ctx.Done():
		cause := context.Cause(ctx)
		cancel(cause)
		<-done
		return fmt.Errorf("while waiting for supervised process to start: %w", cause)
	}
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
func getEnv(dataDir, component string, keepEnvPrefix bool) []string {
	env := os.Environ()
	componentPrefix := fmt.Sprintf("%s_", strings.ToUpper(component))

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
			env[i] = fmt.Sprintf("PATH=%s", dir.PathListJoin(path.Join(dataDir, "bin"), v))
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
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.cmd == nil {
		return nil
	}
	return s.cmd.Process
}

func (s *Supervisor) Signal(sig os.Signal) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.cmd == nil {
		return nil
	}

	return s.cmd.Process.Signal(sig)
}

func (s *Supervisor) clearPIDFile(ctx context.Context) error {
	data, err := os.ReadFile(s.PidFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}

	pid, err := process.ParsePID(string(data))
	if err != nil {
		return fmt.Errorf("not a PID in %q: %w: %s", s.PidFile, err, data)
	}

	h, err := pid.Open()
	if err != nil {
		if errors.Is(err, process.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("failed to open process handle: %w", err)
	}
	defer func() { err = errors.Join(err, h.Close()) }()

	env, err := h.Environ()
	if err != nil {
		return fmt.Errorf("failed to inspect process environment: %w", err)
	}
	if !slices.Contains(env, k0sManaged) {
		return fmt.Errorf("process with PID %v doesn't seem to be supervised by k0s", pid)
	}

	terminated := make(chan error, 1)
	go func() {
		defer close(terminated)
		terminated <- s.awaitTermination(ctx, h)
	}()

	if err := h.Signal(syscall.SIGTERM); err != nil {
		err := fmt.Errorf("failed to send SIGTERM: %w", err)
		if terminated, termErr := h.IsTerminated(); termErr != nil {
			return errors.Join(err, fmt.Errorf("failed to check for process termination: %w", termErr))
		} else if terminated {
			return nil
		}
		s.log.WithError(err).Info("Killing", pid)
	} else {
		s.log.Info("Sent SIGTERM to ", pid, ", awaiting termination")
		select {
		case <-time.After(s.TimeoutStop):
			s.log.Info("Not yet terminated after ", s.TimeoutStop, " - killing ", pid)
		case <-ctx.Done():
			return fmt.Errorf("while awaiting graceful termination: %w", context.Cause(ctx))
		case err := <-terminated:
			return err
		}
	}

	if err := h.Kill(); err != nil {
		return err
	}

	select {
	case <-time.After(s.TimeoutRespawn):
		return errors.New("timed out while waiting for killed process to terminate")
	case <-ctx.Done():
		return fmt.Errorf("while awaiting termination of killed process: %w", context.Cause(ctx))
	case err := <-terminated:
		return err
	}
}

func (s *Supervisor) awaitTermination(ctx context.Context, h process.Handle) error {
	err := h.Wait()

	// Handle doesn't support this for all OSes,
	// i.e. old Linux kernels don't have the required syscalls.
	if !errors.Is(err, errors.ErrUnsupported) {
		return err
	}

	s.log.WithError(err).Debug("Falling back to userspace polling to await process termination")

	backoff := wait.Backoff{
		Duration: 25 * time.Millisecond,
		Cap:      3 * time.Second,
		Steps:    math.MaxInt32,
		Factor:   1.5,
		Jitter:   0.1,
	}

	return wait.ExponentialBackoffWithContext(ctx, backoff, func(context.Context) (bool, error) {
		return h.IsTerminated()
	})
}
