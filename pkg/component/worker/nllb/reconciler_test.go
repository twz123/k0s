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

package nllb

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/k0sproject/k0s/pkg/apis/k0s.k0sproject.io/v1beta1"
	"github.com/k0sproject/k0s/pkg/component/worker"
	"github.com/k0sproject/k0s/pkg/constant"

	corev1 "k8s.io/api/core/v1"

	"github.com/avast/retry-go"
	"github.com/sirupsen/logrus"
	"github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"
)

func TestPodReconciler_ConfigMgmt(t *testing.T) {
	newTestInstance := func(dataDir string) (*NllbReconciler, *staticPodMock) {
		staticPod := new(staticPodMock)
		staticPod.On("Drop").Return()

		staticPods := new(staticPodsMock)
		staticPods.On("ClaimStaticPod", mock.Anything, mock.Anything).Return(staticPod, nil)
		reconciler, err := NewReconciler(
			&constant.CfgVars{DataDir: dataDir},
			staticPods,
			&v1beta1.NodeLocalLoadBalancer{
				Type:       v1beta1.NllbTypeEnvoyProxy,
				EnvoyProxy: v1beta1.DefaultEnvoyProxy(nil),
			},
			1337,
			corev1.PullNever,
		)
		require.NoError(t, err)
		return reconciler, staticPod
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

				underTest, _ := newTestInstance(dataDir)
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

			underTest, _ := newTestInstance(dataDir)
			err := underTest.Init(context.TODO())

			require.Error(t, err)
			assert.True(t, os.IsExist(errors.Unwrap(err)), "expected ErrExist, got %v", err)
		})
	})

	t.Run("configFile", func(t *testing.T) {
		// given
		dataDir := t.TempDir()
		envoyConfig := filepath.Join(dataDir, "nllb", "envoy", "envoy.yaml")
		require.NoError(t, os.MkdirAll(filepath.Join(dataDir, "nllb"), 0700))
		assert.NoError(t, os.WriteFile(
			filepath.Join(dataDir, "nllb", "api-servers.yaml"),
			[]byte(`[{"host":"127.10.10.1", "port": 6443}]`),
			0400,
		))

		// when
		underTest, staticPod := newTestInstance(dataDir)
		t.Cleanup(func() {
			assert.NoError(t, underTest.Stop())
			assert.NoFileExists(t, envoyConfig)
		})
		err := underTest.Init(context.TODO())
		require.NoError(t, err)

		staticPod.On("SetManifest", mock.AnythingOfType("v1.Pod")).Return(nil)
		err = underTest.Start(context.TODO())
		require.NoError(t, err)

		// then
		assert.NoError(t,
			retry.Do(func() error {
				stat, err := os.Stat(envoyConfig)
				if os.IsNotExist(err) {
					return err
				}

				if err == nil && stat.IsDir() {
					err = errors.New("expected a file")
				}

				if err != nil {
					return retry.Unrecoverable(err)
				}

				return nil
			}, retry.LastErrorOnly(true)),
			"Expected to see an Envoy configuration file",
		)

		configBytes, err := os.ReadFile(envoyConfig)
		if assert.NoError(t, err) {
			var yamlConfig any
			assert.NoError(t, yaml.Unmarshal(configBytes, &yamlConfig), "invalid YAML in config file: %s", string(configBytes))
		}
	})
}

func TestPodReconciler_Lifecycle(t *testing.T) {
	log, _ := test.NewNullLogger()
	log.SetLevel(logrus.DebugLevel)

	staticPod := new(staticPodMock)
	staticPod.On("SetManifest", mock.Anything).Return(nil)
	staticPod.On("Drop", mock.Anything).Return()
	staticPods := new(staticPodsMock)
	staticPods.On("ClaimStaticPod", mock.Anything, mock.Anything).Return(staticPod, nil)

	dataDir := t.TempDir()
	underTest, err := NewReconciler(
		&constant.CfgVars{DataDir: dataDir},
		staticPods,
		&v1beta1.NodeLocalLoadBalancer{
			Type: v1beta1.NllbTypeEnvoyProxy,
			EnvoyProxy: &v1beta1.EnvoyProxy{
				Image: v1beta1.DefaultEnvoyProxyImage(nil),
			},
		},
		1337,
		corev1.PullNever,
	)
	require.NoError(t, err)
	underTest.log = log

	t.Run("fails_to_start_without_init", func(t *testing.T) {
		err := underTest.Start(context.TODO())
		require.Error(t, err)
		require.Equal(t, "node_local_load_balancer component is not yet initialized (created)", err.Error())
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

	t.Run("starts", func(runT *testing.T) {
		assert.NoError(t, os.WriteFile(
			filepath.Join(dataDir, "nllb", "api-servers.yaml"),
			[]byte(`[{"host":"127.10.10.1", "port": 6443}]`),
			0400,
		))

		if assert.NoError(runT, underTest.Start(context.TODO())) {
			t.Cleanup(func() { assert.NoError(t, underTest.Stop()) })
		}
	})

	t.Run("another_start_fails", func(t *testing.T) {
		err := underTest.Start(context.TODO())
		require.Error(t, err)
		assert.Equal(t, "node_local_load_balancer component is already started", err.Error())
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

	t.Run("restart_fails", func(t *testing.T) {
		err := underTest.Start(context.TODO())
		require.Error(t, err)
		assert.Equal(t, "node_local_load_balancer component is already stopped", err.Error())
	})
}

type staticPodsMock struct{ mock.Mock }

func (m *staticPodsMock) ManifestURL() (string, error) {
	args := m.Called()
	return args.String(0), args.Error(1)
}

func (m *staticPodsMock) ClaimStaticPod(namespace, name string) (worker.StaticPod, error) {
	args := m.Called(namespace, name)
	return args.Get(0).(worker.StaticPod), args.Error(1)
}

type staticPodMock struct{ mock.Mock }

func (m *staticPodMock) SetManifest(podResource interface{}) error {
	args := m.Called(podResource)
	return args.Error(0)
}

func (m *staticPodMock) Clear() {
	m.Called()
}

func (m *staticPodMock) Drop() {
	m.Called()
}

func TestPodReconciler_EnvoyBootstrapConfig_Template(t *testing.T) {
	var buf bytes.Buffer

	for _, test := range []struct {
		name     string
		expected int
		servers  []hostPort
	}{
		{"empty", 0, []hostPort{}},
		{"one", 1, []hostPort{{"foo", 16}}},
		{"two", 2, []hostPort{{"foo", 16}, {"bar", 17}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var state podState
			state.Shared.LBAddr = net.IPv6loopback
			state.LoadBalancer.UpstreamServers = test.servers
			assert.NoError(t, envoyBootstrapConfig.Execute(&buf, &state))
			t.Logf("rendered: %s", buf.String())

			var parsed jo
			require.NoError(t, yaml.Unmarshal(buf.Bytes(), &parsed), "invalid YAML in envoy config")

			ip := parsed.o("static_resources").a("listeners").o(0).o("address").o("socket_address")["address"]
			assert.Equal(t, "::1", ip)

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
