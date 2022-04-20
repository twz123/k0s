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

package worker

import (
	"context"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestNodeLocalLoadBalancer_Lifecycle(t *testing.T) {
	log, _ := test.NewNullLogger()
	log.SetLevel(logrus.DebugLevel)

	staticPod := new(nllbStaticPodMock)
	staticPod.On("SetManifest", mock.Anything).Return(nil)
	staticPod.On("Drop", mock.Anything).Return()
	staticPods := new(nllbStaticPodsMock)
	staticPods.On("ClaimStaticPod", mock.Anything, mock.Anything).Return(staticPod, nil)

	underTest := NewNodeLocalLoadBalancer(staticPods)
	underTest.(*nodeLocalLoadBalancer).log = log

	t.Run("fails_to_run_without_init", func(t *testing.T) {
		err := underTest.Run(context.TODO())
		require.Error(t, err)
		require.Equal(t, "node_local_load_balancer component is not yet initialized (created)", err.Error())
	})

<<<<<<< Updated upstream
	t.Run("health_check_fails_without_init", func(t *testing.T) {
		err := underTest.Healthy()
=======
	t.Run("fails_to_stop_without_init", func(t *testing.T) {
		err := underTest.Stop()
>>>>>>> Stashed changes
		require.Error(t, err)
		require.Equal(t, "node_local_load_balancer component is not yet running (created)", err.Error())
	})

	t.Run("fails_to_stop_without_init", func(t *testing.T) {
		err := underTest.Stop()
		require.Error(t, err)
		require.Equal(t, "node_local_load_balancer component is not yet running (created)", err.Error())
	})

	t.Run("init", func(t *testing.T) {
		require.NoError(t, underTest.Init(context.TODO()))
	})

	t.Run("another_init_fails", func(t *testing.T) {
		err := underTest.Init(context.TODO())
		if assert.Error(t, err) {
			assert.Equal(t, "node_local_load_balancer component is already initialized", err.Error())
		}
	})

<<<<<<< Updated upstream
	t.Run("health_check_fails_before_run", func(t *testing.T) {
		err := underTest.Healthy()
		require.Error(t, err)
		require.Equal(t, "node_local_load_balancer component is not yet running (initialized)", err.Error())
=======
	t.Run("stop_before_run_fails", func(t *testing.T) {
		err := underTest.Stop()
		require.Error(t, err)
		assert.Equal(t, "node_local_load_balancer component is not yet running (initialized)", err.Error())
>>>>>>> Stashed changes
	})

	t.Run("stop_before_run_fails", func(t *testing.T) {
		err := underTest.Stop()
		require.Error(t, err)
		assert.Equal(t, "node_local_load_balancer component is not yet running (initialized)", err.Error())
	})

	t.Run("runs", func(runT *testing.T) {
		if assert.NoError(runT, underTest.Run(context.TODO())) {
			t.Cleanup(func() { assert.NoError(t, underTest.Stop()) })
		}
	})

	t.Run("another_run_fails", func(t *testing.T) {
		err := underTest.Run(context.TODO())
		require.Error(t, err)
		assert.Equal(t, "node_local_load_balancer component is already running", err.Error())
	})

	t.Run("stops", func(t *testing.T) {
		require.NoError(t, underTest.Stop())
	})

<<<<<<< Updated upstream
	t.Run("health_check_fails_after_stopped", func(t *testing.T) {
		err := underTest.Healthy()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "connection refused")
	})

=======
>>>>>>> Stashed changes
	t.Run("stop_may_be_called_again", func(t *testing.T) {
		require.NoError(t, underTest.Stop())
	})

	t.Run("reinit_fails", func(t *testing.T) {
		err := underTest.Init(context.TODO())
		require.Error(t, err)
		assert.Equal(t, "node_local_load_balancer component is already stopped", err.Error())
	})

	t.Run("rerun_fails", func(t *testing.T) {
		err := underTest.Run(context.TODO())
		require.Error(t, err)
		assert.Equal(t, "node_local_load_balancer component is already stopped", err.Error())
	})
}

type nllbStaticPodsMock struct{ mock.Mock }

func (m *nllbStaticPodsMock) ManifestURL() (string, error) {
	args := m.Called()
	return args.String(0), args.Error(1)
}

func (m *nllbStaticPodsMock) ClaimStaticPod(namespace, name string) (StaticPod, error) {
	args := m.Called(namespace, name)
	return args.Get(0).(StaticPod), args.Error(1)
}

type nllbStaticPodMock struct{ mock.Mock }

func (m *nllbStaticPodMock) SetManifest(podResource interface{}) error {
	args := m.Called(podResource)
	return args.Error(0)
}

func (m *nllbStaticPodMock) Clear() {
	m.Called()
}

func (m *nllbStaticPodMock) Drop() {
	m.Called()
}
