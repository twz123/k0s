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
	"os"
	"testing"

	"github.com/k0sproject/k0s/internal/os/process"

	"github.com/k0sproject/k0s/internal/testutil/pingpong"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandle_Kill(t *testing.T) {
	cmd, pingPong := pingpong.Start(t)
	t.Cleanup(func() { _, _ = cmd.Process.Kill(), cmd.Wait() })

	// Wait until the process is running
	require.NoError(t, pingPong.AwaitPing())

	// Open process handle
	underTest, err := process.OpenPID(uint(cmd.Process.Pid))
	require.NoError(t, err)

	require.NoError(t, underTest.Kill())

	assert.ErrorContains(t, cmd.Wait(), "signal: killed")
}

func TestHandle_Environ(t *testing.T) {
	var rnd [16]byte
	_, err := rand.Read(rnd[:])
	require.NoError(t, err)
	marker := "_K0S_OS_PROCESS_HANDLE_TEST_MARKER=" + hex.EncodeToString(rnd[:])

	cmd, pingPong := pingpong.Start(t, pingpong.StartOptions{
		Env: []string{marker},
	})

	// Wait until the process is running
	require.NoError(t, pingPong.AwaitPing())

	// Open process handle
	underTest, err := process.OpenPID(uint(cmd.Process.Pid))
	require.NoError(t, err)

	env, err := underTest.Environ()
	require.NoError(t, err)

	for _, v := range env {
		t.Logf("Environ: %q", v)
	}

	assert.Contains(t, env, marker)
}

func TestHandle_Waited(t *testing.T) {
	cmd, pingPong := pingpong.Start(t)

	require.NoError(t, pingPong.AwaitPing())

	underTest, err := process.OpenPID(uint(cmd.Process.Pid))
	require.NoError(t, err)

	require.NoError(t, pingPong.SendPong())
	require.NoError(t, cmd.Wait())

	t.Run("Environ", func(t *testing.T) {
		_, err := underTest.Environ()
		assert.ErrorIs(t, err, process.ErrTerminated)
	})

	t.Run("Kill", func(t *testing.T) {
		err := underTest.Kill()
		assert.ErrorIs(t, err, process.ErrTerminated)
	})

	t.Run("Signal", func(t *testing.T) {
		err := underTest.Signal(os.Kill)
		assert.ErrorIs(t, err, process.ErrTerminated)
	})

	t.Run("IsTerminated", func(t *testing.T) {
		terminated, err := underTest.IsTerminated()
		assert.NoError(t, err)
		assert.True(t, terminated)
	})
}
