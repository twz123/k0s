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
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/k0sproject/k0s/pkg/constant"
	"github.com/sirupsen/logrus"
	"github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"
)

func TestNodeLocalLoadBalancer_ConfigMgmt(t *testing.T) {
	newTestInstance := func(dataDir string) *NodeLocalLoadBalancer {
		staticPod := new(nllbStaticPodMock)
		staticPod.On("Drop").Return()

		staticPods := new(nllbStaticPodsMock)
		staticPods.On("ClaimStaticPod", mock.Anything, mock.Anything).Return(staticPod, nil)
		return NewNodeLocalLoadBalancer(&constant.CfgVars{DataDir: dataDir}, nil, staticPods)
	}

	t.Run("configDir", func(t *testing.T) {
		for _, test := range []struct {
			name    string
			prepare func(t *testing.T, dir string)
		}{
			{"create", func(t *testing.T, dir string) {}},
			{"chmod", func(t *testing.T, dir string) { require.NoError(t, os.Mkdir(dir, 0777)) }},
		} {
			t.Run(test.name, func(t *testing.T) {
				dataDir := t.TempDir()
				nllbDir := filepath.Join(dataDir, "nllb")
				test.prepare(t, nllbDir)

				underTest := newTestInstance(dataDir)
				err := underTest.Init(context.TODO())
				require.NoError(t, err)

				stat, err := os.Stat(nllbDir)
				require.NoError(t, err)
				assert.True(t, stat.IsDir())
				assert.Equal(t, 0700, int(stat.Mode()&fs.ModePerm))
			})
		}

		t.Run("obstructed", func(t *testing.T) {
			dataDir := t.TempDir()
			nllbDir := filepath.Join(dataDir, "nllb")
			require.NoError(t, os.WriteFile(nllbDir, []byte("obstructed"), 0777))

			underTest := newTestInstance(dataDir)
			err := underTest.Init(context.TODO())

			require.Error(t, err)
			assert.True(t, os.IsExist(errors.Unwrap(err)), "expected ErrExist, got %v", err)
		})
	})

	t.Run("configFile", func(t *testing.T) {
		// given
		dataDir := t.TempDir()
		envoyConfig := filepath.Join(dataDir, "nllb", "envoy", "envoy.yaml")

		// when
		underTest := newTestInstance(dataDir)
		t.Cleanup(func() {
			assert.NoError(t, underTest.Stop())
			assert.NoFileExists(t, envoyConfig)
		})
		err := underTest.Init(context.TODO())
		require.NoError(t, err)
		err = underTest.Run(context.TODO())
		require.NoError(t, err)

		// then
		assert.FileExists(t, envoyConfig)
		configBytes, err := os.ReadFile(envoyConfig)
		if assert.NoError(t, err) {
			var yamlConfig any
			assert.NoError(t, yaml.Unmarshal(configBytes, &yamlConfig), "invalid YAML in config file")
		}

	})
}

func TestNodeLocalLoadBalancer_Lifecycle(t *testing.T) {
	log, _ := test.NewNullLogger()
	log.SetLevel(logrus.DebugLevel)

	staticPod := new(nllbStaticPodMock)
	staticPod.On("SetManifest", mock.Anything).Return(nil)
	staticPod.On("Drop", mock.Anything).Return()
	staticPods := new(nllbStaticPodsMock)
	staticPods.On("ClaimStaticPod", mock.Anything, mock.Anything).Return(staticPod, nil)

	underTest := NewNodeLocalLoadBalancer(&constant.CfgVars{DataDir: t.TempDir()}, nil, staticPods)
	underTest.log = log

	t.Run("fails_to_run_without_init", func(t *testing.T) {
		err := underTest.Run(context.TODO())
		require.Error(t, err)
		require.Equal(t, "node_local_load_balancer component is not yet initialized (created)", err.Error())
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

func TestNodeLocalLoadBalancer_EnvoyBootstrapConfig_Template(t *testing.T) {
	var buf bytes.Buffer

	for _, test := range []struct {
		name     string
		expected int
		servers  []nllbHostPort
	}{
		{"empty", 0, []nllbHostPort{}},
		{"one", 1, []nllbHostPort{{"foo", 16}}},
		{"two", 2, []nllbHostPort{{"foo", 16}, {"bar", 17}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var state nllbRecoState
			state.LoadBalancer.UpstreamServers = test.servers
			assert.NoError(t, envoyBootstrapConfig.Execute(&buf, &state))
			t.Logf("rendered: %s", buf.String())

			var parsed jo
			require.NoError(t, yaml.Unmarshal(buf.Bytes(), &parsed), "invalid YAML in envoy config")

			eps := parsed.o("static_resources").a("clusters").o(0).o("load_assignment").a("endpoints").o(0).a("lb_endpoints")
			assert.Len(t, eps, test.expected)
		})
	}
}

type jo map[string]interface{}
type ja []interface{}

func (o jo) o(key string) jo {
	return jo(o[key].(map[string]interface{}))
}

func (o jo) a(key string) ja {
	return ja(o[key].([]interface{}))
}

func (a ja) o(key uint) jo {
	return jo(a[key].(map[string]interface{}))
}

// func (a ja) a(key uint) ja {
// 	return ja(a[key].([]interface{}))
// }
