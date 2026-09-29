//go:build unix

// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package status_test

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/k0sproject/k0s/pkg/component/status"
	kubeutil "github.com/k0sproject/k0s/pkg/kubernetes"

	"k8s.io/client-go/rest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStatus_APIConnectionProbe(t *testing.T) {
	// An API server that fails every request, so that probes fail fast.
	apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not today", http.StatusInternalServerError)
	}))
	t.Cleanup(apiServer.Close)

	var configLoads atomic.Int32
	clientFactory := &kubeutil.ClientFactory{LoadRESTConfig: func() (*rest.Config, error) {
		configLoads.Add(1)
		return &rest.Config{Host: apiServer.URL}, nil
	}}

	underTest := status.Status{
		StatusInformation:      status.K0sStatus{Role: "worker", Workloads: true},
		GetWorkerClientFactory: func() kubeutil.ClientFactoryInterface { return clientFactory },
		Socket:                 filepath.Join(t.TempDir(), "status.sock"),
	}
	require.NoError(t, underTest.Init(t.Context()))
	require.NoError(t, underTest.Start(t.Context()))
	t.Cleanup(func() { assert.NoError(t, underTest.Stop()) })

	t.Run("skips the probe on request", func(t *testing.T) {
		info, err := status.GetStatusInfoWithoutProbe(underTest.Socket)
		require.NoError(t, err)
		assert.Equal(t, "worker", info.Role)
		assert.True(t, info.Workloads)
		assert.Zero(t, info.WorkerToAPIConnectionStatus)
		assert.Zero(t, configLoads.Load(), "Expected the API connection not to be probed")
	})

	t.Run("probes by default", func(t *testing.T) {
		info, err := status.GetStatusInfo(underTest.Socket)
		require.NoError(t, err)
		assert.Equal(t, "worker", info.Role)
		assert.True(t, info.Workloads)
		assert.False(t, info.WorkerToAPIConnectionStatus.Success)
		assert.NotEmpty(t, info.WorkerToAPIConnectionStatus.Message)
		assert.Equal(t, int32(1), configLoads.Load(), "Expected the API connection to be probed")
	})
}
