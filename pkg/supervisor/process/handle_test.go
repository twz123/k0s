//go:build linux || windows

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

package process_test

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"runtime"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/k0sproject/k0s/internal/testutil/pingpong"
	"github.com/k0sproject/k0s/pkg/supervisor/process"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandle_Kill(t *testing.T) {
	cmd, pingPong := pingpong.Start(t)
	t.Cleanup(func() { _, _ = cmd.Process.Kill(), cmd.Wait() })

	// Wait until the process is running
	require.NoError(t, pingPong.AwaitPing())

	// Open process handle
	underTest, err := process.Open(cmd.Process)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, underTest.Close()) })

	require.NoError(t, underTest.Kill())

	err = cmd.Wait()
	switch runtime.GOOS {
	case "windows":
		assert.ErrorContains(t, err, "exit status 137")
	default:
		assert.ErrorContains(t, err, "signal: killed")
	}
}

func TestHandle_Environ(t *testing.T) {
	var rnd [16]byte
	_, err := rand.Read(rnd[:])
	require.NoError(t, err)
	marker := "_K0S_SUPERVISOR_PROCESS_TEST_MARKER=" + hex.EncodeToString(rnd[:])

	cmd, pingPong := pingpong.Start(t, pingpong.StartOptions{
		Env: []string{marker},
	})

	// Wait until the process is running
	require.NoError(t, pingPong.AwaitPing())

	// Open process handle
	underTest, err := process.Open(cmd.Process)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, underTest.Close()) })

	env, err := underTest.Environ()
	require.NoError(t, err)

	for _, v := range env {
		t.Logf("Environ: %q", v)
	}

	assert.Contains(t, env, marker)
}

func TestHandle_Wait_IsTerminated(t *testing.T) {
	cmd, pingPong := pingpong.Start(t)
	var pongSent atomic.Bool
	t.Cleanup(func() {
		if !pongSent.Load() && assert.NoError(t, pingPong.SendPong()) {
			assert.NoError(t, cmd.Wait())
		}
	})

	// Wait until the process is running
	require.NoError(t, pingPong.AwaitPing())

	// Open process handle
	underTest, err := process.Open(cmd.Process)
	require.NoError(t, err)

	if done, err := underTest.IsTerminated(); assert.NoError(t, err) {
		assert.False(t, done, "Process should still be running.")
	}

	// Ensure that Wait is blocking
	handleWaitStarts := make(chan struct{})
	handleWaitDone := make(chan error, 1)
	go func() {
		defer close(handleWaitDone)
		close(handleWaitStarts)
		err := underTest.Wait()
		if err == nil {
			assert.True(t, pongSent.Load(), "Wait returned before pong was sent.")
		} else {
			handleWaitDone <- err
		}
	}()
	t.Cleanup(func() {
		assert.NoError(t, underTest.Close())
		<-handleWaitDone
	})

	<-handleWaitStarts
	select {
	case <-time.After(100 * time.Millisecond):
	case err := <-handleWaitDone:
		if errors.Is(err, errors.ErrUnsupported) {
			t.Skip("This test can't be performed on this platform:", err)
		}
		require.NoError(t, err, "Wait failed.")
		require.Fail(t, "Expected Wait to be ongoing")
	}

	// Send a pong, so that the process exits.
	pongSent.Store(true)
	require.NoError(t, pingPong.SendPong())

	select {
	case <-time.After(3 * time.Second):
		assert.Fail(t, "Expected Wait to be returning after process has exited")
	case <-handleWaitDone:
		if done, err := underTest.IsTerminated(); assert.NoError(t, err) {
			assert.True(t, done, "Process should be terminated after Wait returned.")
		}
	}

	assert.NoError(t, cmd.Wait())
}

func TestHandle_Wait_Close(t *testing.T) {
	cmd, pingPong := pingpong.Start(t)

	// Wait until the process is running
	require.NoError(t, pingPong.AwaitPing())

	// Open process handle
	underTest, err := process.Open(cmd.Process)
	require.NoError(t, err)

	// Ensure that Wait is interrupted when handle is closed.
	waitStarts, waitErr := make(chan struct{}), make(chan error)
	go func() { close(waitStarts); waitErr <- underTest.Wait() }()

	<-waitStarts
	select {
	case <-time.After(100 * time.Millisecond):
	case err := <-waitErr:
		if errors.Is(err, errors.ErrUnsupported) {
			t.Skip("This test can't be performed on this platform:", err)
		}
		require.Fail(t, "Expected Wait to be ongoing", err.Error())
	}

	require.NoError(t, underTest.Close())

	select {
	case err := <-waitErr:
		switch runtime.GOOS {
		case "windows":
			assert.ErrorContains(t, err, "The handle is invalid.")
		default:
			assert.ErrorIs(t, err, fs.ErrClosed, "Expected Wait to return a closed error.")
		}
	case <-time.After(3 * time.Second):
		assert.Fail(t, "Expected Wait to be returning after handle has been closed.")
	}
}

func TestHandle_Terminated(t *testing.T) {
	cmd, pingPong := pingpong.Start(t)
	var stopped bool
	t.Cleanup(func() {
		if stopped || assert.NoError(t, pingPong.SendPong()) {
			assert.NoError(t, cmd.Wait())
		}
	})

	require.NoError(t, pingPong.AwaitPing())

	underTest, err := process.Open(cmd.Process)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, underTest.Close()) })

	stopped = true
	require.NoError(t, pingPong.SendPong())

	require.Eventually(t, func() bool {
		terminated, err := underTest.IsTerminated()
		require.NoError(t, err)
		return terminated
	}, 10*time.Second, 100*time.Millisecond)

	t.Run("Signal", func(t *testing.T) {
		err := underTest.Signal(os.Kill)
		// This is a difference between Linux and Windows where it's not clear
		// how process.Handle could abstract away platform specific differences
		// in a meaningful way. On Linux, there's zombie processes, i.e.
		// processes that terminated, but weren't reaped yet. Such processes can
		// be signalled just fine. Windows doesn't have signals. SIGKILL is
		// usually translated to the TerminateProcess syscall, and that only
		// works once.
		switch runtime.GOOS {
		case "windows":
			assert.ErrorIs(t, err, process.ErrTerminated)
		default:
			assert.NoError(t, err, "Signal should succeed for terminated processes as long as they weren't reaped.")
		}
	})

	t.Run("Environ", func(t *testing.T) {
		_, err := underTest.Environ()
		assert.ErrorIs(t, err, process.ErrTerminated)
	})

	t.Run("Wait", func(t *testing.T) {
		err := underTest.Wait()
		if errors.Is(err, errors.ErrUnsupported) {
			t.Skip("This test can't be performed on this platform:", err)
		}
		assert.NoError(t, err)
	})
}

func TestHandle_Reaped(t *testing.T) {
	cmd, pingPong := pingpong.Start(t)

	require.NoError(t, pingPong.AwaitPing())

	underTest, err := process.Open(cmd.Process)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, underTest.Close()) })

	require.NoError(t, pingPong.SendPong())
	require.NoError(t, cmd.Wait())

	t.Run("Signal", func(t *testing.T) {
		err := underTest.Signal(os.Kill)
		require.ErrorIs(t, err, process.ErrTerminated)
	})

	t.Run("Environ", func(t *testing.T) {
		_, err := underTest.Environ()
		require.ErrorIs(t, err, process.ErrTerminated)
	})

	t.Run("Wait", func(t *testing.T) {
		err := underTest.Wait()
		if errors.Is(err, errors.ErrUnsupported) {
			t.Skip("This test can't be performed on this platform:", err)
		}
		assert.NoError(t, err)
	})
}

func TestHandle_AfterClose(t *testing.T) {
	cmd, pingPong := pingpong.Start(t)

	require.NoError(t, pingPong.AwaitPing())

	pid := process.PID(cmd.Process.Pid)
	require.Equal(t, cmd.Process.Pid, int(pid))

	underTest, err := pid.Open()
	require.NoError(t, err)

	require.NoError(t, underTest.Close())

	expectedClosedErr := fs.ErrClosed
	if runtime.GOOS == "windows" {
		expectedClosedErr = syscall.Errno(6) // == windows.ERROR_INVALID_HANDLE
	}

	t.Run("Close", func(t *testing.T) {
		err := underTest.Close()
		assert.ErrorIs(t, err, expectedClosedErr)
	})

	t.Run("Signal", func(t *testing.T) {
		err := underTest.Signal(syscall.Signal(0))
		assert.ErrorIs(t, err, expectedClosedErr)
	})

	t.Run("Environ", func(t *testing.T) {
		_, err := underTest.Environ()
		assert.ErrorIs(t, err, expectedClosedErr)
	})

	t.Run("Wait", func(t *testing.T) {
		err := underTest.Wait()
		if errors.Is(err, errors.ErrUnsupported) {
			t.Skip("This test can't be performed on this platform:", err)
		}
		assert.ErrorIs(t, err, expectedClosedErr)
	})

	t.Run("IsTerminated", func(t *testing.T) {
		_, err := underTest.IsTerminated()
		assert.ErrorIs(t, err, expectedClosedErr)
	})
}
