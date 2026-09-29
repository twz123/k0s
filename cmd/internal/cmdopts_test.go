//go:build unix

// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package internal_test

import (
	"path/filepath"
	"testing"

	"github.com/k0sproject/k0s/cmd/internal"
	"github.com/k0sproject/k0s/pkg/component/status"
	"github.com/k0sproject/k0s/pkg/config"

	"github.com/spf13/cobra"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetCmdOpts(t *testing.T) {
	t.Run("uses the running instance's variables", func(t *testing.T) {
		instanceVars := &config.CfgVars{DataDir: filepath.Join(t.TempDir(), "instance")}
		socketPath := serveStatus(t, status.K0sStatus{Role: "controller", K0sVars: instanceVars})

		opts, err := internal.GetCmdOpts(newCmd(t, "--data-dir", t.TempDir(), "--status-socket", socketPath))
		require.NoError(t, err)
		assert.Equal(t, instanceVars, opts.K0sVars)
		if assert.NotNil(t, opts.Status) {
			assert.Equal(t, "controller", opts.Status.Role)
		}
	})

	t.Run("uses the flags if nothing is running", func(t *testing.T) {
		dataDir := t.TempDir()
		socketPath := filepath.Join(t.TempDir(), "status.sock")

		opts, err := internal.GetCmdOpts(newCmd(t, "--data-dir", dataDir, "--status-socket", socketPath))
		require.NoError(t, err)
		assert.Equal(t, dataDir, opts.K0sVars.DataDir)
		assert.Equal(t, socketPath, opts.K0sVars.StatusSocketPath)
		assert.Nil(t, opts.Status)
	})

	t.Run("uses the flags if the running instance has no variables", func(t *testing.T) {
		dataDir := t.TempDir()
		socketPath := serveStatus(t, status.K0sStatus{Role: "controller"})

		opts, err := internal.GetCmdOpts(newCmd(t, "--data-dir", dataDir, "--status-socket", socketPath))
		require.NoError(t, err)
		assert.Equal(t, dataDir, opts.K0sVars.DataDir)
		if assert.NotNil(t, opts.Status) {
			assert.Equal(t, "controller", opts.Status.Role)
		}
	})
}

func newCmd(t *testing.T, args ...string) *cobra.Command {
	cmd := &cobra.Command{Use: "test"}
	cmd.Flags().AddFlagSet(config.GetPersistentFlagSet())
	require.NoError(t, cmd.ParseFlags(args))
	return cmd
}

// Serves the given status on a temporary socket and returns the socket path.
func serveStatus(t *testing.T, k0sStatus status.K0sStatus) string {
	s := status.Status{
		StatusInformation: k0sStatus,
		Socket:            filepath.Join(t.TempDir(), "status.sock"),
	}
	require.NoError(t, s.Init(t.Context()))
	require.NoError(t, s.Start(t.Context()))
	t.Cleanup(func() { assert.NoError(t, s.Stop()) })
	return s.Socket
}
