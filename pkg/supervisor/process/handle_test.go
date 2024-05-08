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
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/k0sproject/k0s/pkg/supervisor/process"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	if _, err := newSleepCommand(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}

	os.Exit(m.Run())
}

func TestHandle_Signal_Kill(t *testing.T) {
	cmd := makeSleepCommand(t)
	require.NoError(t, cmd.Start())

	underTest, err := process.Open(cmd.Process)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, underTest.Close()) })

	require.NoError(t, underTest.Signal(os.Kill))

	state, err := cmd.Process.Wait()
	require.NoError(t, err)
	require.False(t, state.Success())
}

func TestHandle_Environ(t *testing.T) {
	cmd := makeSleepCommand(t)

	var rnd [16]byte
	_, err := rand.Read(rnd[:])
	require.NoError(t, err)
	marker := "_K0S_SUPERVISOR_PROCESS_TEST_MARKER=" + hex.EncodeToString(rnd[:])

	cmd.Env = []string{marker}
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		if assert.NoError(t, cmd.Process.Kill()) {
			_, err := cmd.Process.Wait()
			assert.NoError(t, err)
		}
	})

	underTest, err := process.Open(cmd.Process)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, underTest.Close()) })

	var env []string

	// Give the process a bit of startup time.
	// FIXME: does this make any sense?
	assert.Eventually(t, func() bool {
		env, err = underTest.Environ()
		require.NoError(t, err)
		return len(env) > 0
	}, 1*time.Second, 10*time.Millisecond)

	for _, v := range env {
		t.Logf("Environ: %q", v)
	}

	assert.Contains(t, env, marker)
}

func TestHandle_AfterExit(t *testing.T) {
	cmd := makeSleepCommand(t)
	require.NoError(t, cmd.Start())
	var closed bool
	t.Cleanup(func() {
		if !closed {
			if assert.NoError(t, cmd.Process.Kill()) {
				_, err := cmd.Process.Wait()
				assert.NoError(t, err)
			}
		}
	})

	underTest, err := process.Open(cmd.Process)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, underTest.Close()) })

	require.NoError(t, cmd.Process.Kill())
	closed = true
	_, err = cmd.Process.Wait()
	require.NoError(t, err)

	t.Run("Signal", func(t *testing.T) {
		err := underTest.Signal(os.Kill)
		require.ErrorIs(t, err, process.ErrGone)
	})

	t.Run("Environ", func(t *testing.T) {
		_, err := underTest.Environ()
		require.ErrorIs(t, err, process.ErrGone)
	})
}

func TestHandle_AfterClose(t *testing.T) {
	cmd := makeSleepCommand(t)

	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		if assert.NoError(t, cmd.Process.Kill()) {
			_, err := cmd.Process.Wait()
			assert.NoError(t, err)
		}
	})

	pid := process.PID(cmd.Process.Pid)
	require.Equal(t, cmd.Process.Pid, int(pid))

	underTest, err := process.OpenHandle(pid)
	require.NoError(t, err)

	require.NoError(t, underTest.Close())

	t.Run("Close", func(t *testing.T) {
		err := underTest.Close()
		require.ErrorIs(t, err, syscall.EINVAL)
	})

	t.Run("Signal", func(t *testing.T) {
		err := underTest.Signal(os.Kill)
		require.ErrorIs(t, err, syscall.EINVAL)
	})

	t.Run("Environ", func(t *testing.T) {
		_, err := underTest.Environ()
		require.ErrorIs(t, err, syscall.EINVAL)
	})
}

func makeSleepCommand(t *testing.T) *exec.Cmd {
	cmd, err := newSleepCommand()
	require.NoError(t, err)
	return cmd
}

func newSleepCommand() (*exec.Cmd, error) {
	if _, err := exec.LookPath("sleep"); err == nil {
		return exec.Command("sleep", "60"), nil
	}

	if _, err := exec.LookPath("powershell"); err == nil {
		return exec.Command("powershell", "-noprofile", "-noninteractive", "-command", "Start-Sleep -Seconds 60"), nil
	}

	return nil, errors.New("neither sleep nor powershell in PATH, dunno how to create a dummy process")
}
