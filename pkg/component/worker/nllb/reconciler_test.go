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
	"os"
	"path/filepath"
	"testing"

	"github.com/k0sproject/k0s/pkg/apis/k0s.k0sproject.io/v1beta1"
	"github.com/k0sproject/k0s/pkg/component/worker"
	"github.com/k0sproject/k0s/pkg/constant"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/yaml"

	"github.com/sirupsen/logrus"
	"github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

const foo = `
apiVersion: v1
clusters:
- cluster:
    certificate-authority-data: LS0tLS1CRUdJTiBDRVJUSUZJQ0FURS0tLS0tCk1JSURBRENDQWVpZ0F3SUJBZ0lVSjZiUG5GQVROd2xRaGp4THJ6R2FiSElaRHFNd0RRWUpLb1pJaHZjTkFRRUwKQlFBd0dERVdNQlFHQTFVRUF4TU5hM1ZpWlhKdVpYUmxjeTFqWVRBZUZ3MHlNakE0TXpFd05qTTBNREJhRncwegpNakE0TWpnd05qTTBNREJhTUJneEZqQVVCZ05WQkFNVERXdDFZbVZ5Ym1WMFpYTXRZMkV3Z2dFaU1BMEdDU3FHClNJYjNEUUVCQVFVQUE0SUJEd0F3Z2dFS0FvSUJBUUNqZGdNcENaQjRzalAxNlNIYk5RUGxKY3JnYk9tcmNvRzAKcTVTQ1J3aDQ0a3c1dStrKzREUUg2S2FRcVlTTnUxdmtNTTRjQlNPRWJKVTFWSWczZlRqRnFGL3ZkYUwwSkpXWAplazc3bzlld01pZzl2VGpOelN0aXd1U3BEckR2Zld5RGVrc3hSaGIvSmxiMTV2clRZbFlmUkJVaXVUTE9LL3NJCkZFWTlVTzZXU2duR2dhR3dGRjIyRnFVMFc0MWozVi93Rjd5SWhJSnF3dlo3THE4NnAzdzNSTmZ5RzhZODI1REQKZlNrTThYNDV2QXREb25haDI0Y1VkUWdkSWJGNE9LSDU4bHJMUE92R1VwMDh4d3B0OXJQUmJiVG00d010TkVSawptb1dDbklBR2I0alREQjhaVElJenhjZTFsS0wwUmV2UU40SlJ1WkI3RWJzNjRBZnFlMFdqQWdNQkFBR2pRakJBCk1BNEdBMVVkRHdFQi93UUVBd0lCQmpBUEJnTlZIUk1CQWY4RUJUQURBUUgvTUIwR0ExVWREZ1FXQkJUa0hUZS8KM0FRNEk2MXNlMmRKUWZ3NHJDdHEwREFOQmdrcWhraUc5dzBCQVFzRkFBT0NBUUVBTXVsMTJjRVp6U1c5L2RsKwpEWkxETi8zcURuMFE1T1EyaHBPNXIrUFFCaThIeWdzb3ducFhlWlNOZ3YxS0ZzVjl6MjNybzBsSVY2Q3k5U0s5CkRQU0huTWlEMVpSemgwbE5yMjB0RE1jeTR3Z2V5TmFjUFdJcnNVL25ZWlkyK0xKMTZpOE9pQTNMV0xPaGlvUkwKd2xKOWltMldpVW0vNFdVRG5VZnNZQmdxQ0htMjNadlRSb3NnVWNxaWdJR2g5SEVLZnNhcHBTU2R1SzBNMjhHZApqY2lmaEVmQnQyV1pSNGlZd2M1YStHNEZEc2NGUW9SRkc1SGFab2EvbzVLZklLMXZVa0xnbVVDTFhUd2E5UnhkCkp4T2QvRmJWTVA2cVJNa09vYlFLbmpNNklScGd6NHA5bWdWaTFFOHE3dFV0L1VGVkpwZTJVWkdJRmo4dWpkeE0KbnpxSDhRPT0KLS0tLS1FTkQgQ0VSVElGSUNBVEUtLS0tLQo=
    server: https://10.70.123.30:6443
  name: default-cluster
contexts:
- context:
    cluster: default-cluster
    namespace: default
    user: default-auth
  name: default-context
current-context: default-context
kind: Config
preferences: {}
users:
- name: default-auth
#  user:
#    client-certificate: /var/lib/k0s/kubelet/pki/kubelet-client-current.pem
#    client-key: /var/lib/k0s/kubelet/pki/kubelet-client-current.pem
`

func TestFoo(t *testing.T) {

	f := filepath.Join(t.TempDir(), "kubeconfig")
	require.NoError(t, os.WriteFile(f, []byte(foo), 0400))

	var x *clientcmd.ConfigOverrides
	// x := &clientcmd.ConfigOverrides{ClusterInfo: clientcmdapi.Cluster{Server: ""}}
	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		&clientcmd.ClientConfigLoadingRules{ExplicitPath: f}, x).ClientConfig()
	require.NoError(t, err)

	t.Log(cfg.Host)
	t.Log(cfg.APIPath)

	t.Fail()
}

func TestPodReconciler_ConfigMgmt(t *testing.T) {
	newTestInstance := func(dataDir string) *Reconciler {
		staticPod := new(nllbStaticPodMock)
		staticPod.On("Drop").Return()

		staticPods := new(nllbStaticPodsMock)
		staticPods.On("ClaimStaticPod", mock.Anything, mock.Anything).Return(staticPod, nil)
		return NewReconciler(&constant.CfgVars{DataDir: dataDir}, staticPods, v1beta1.ImageSpec{}, corev1.PullNever)
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
		err = underTest.Start(context.TODO())
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

func TestPodReconciler_Lifecycle(t *testing.T) {
	log, _ := test.NewNullLogger()
	log.SetLevel(logrus.DebugLevel)

	staticPod := new(nllbStaticPodMock)
	staticPod.On("SetManifest", mock.Anything).Return(nil)
	staticPod.On("Drop", mock.Anything).Return()
	staticPods := new(nllbStaticPodsMock)
	staticPods.On("ClaimStaticPod", mock.Anything, mock.Anything).Return(staticPod, nil)

	underTest := NewReconciler(&constant.CfgVars{DataDir: t.TempDir()}, staticPods, v1beta1.ImageSpec{}, corev1.PullNever)
	underTest.log = log

	t.Run("fails_to_run_without_init", func(t *testing.T) {
		err := underTest.Start(context.TODO())
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
		if assert.NoError(runT, underTest.Start(context.TODO())) {
			t.Cleanup(func() { assert.NoError(t, underTest.Stop()) })
		}
	})

	t.Run("another_run_fails", func(t *testing.T) {
		err := underTest.Start(context.TODO())
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
		err := underTest.Start(context.TODO())
		require.Error(t, err)
		assert.Equal(t, "node_local_load_balancer component is already stopped", err.Error())
	})
}

type nllbStaticPodsMock struct{ mock.Mock }

func (m *nllbStaticPodsMock) ManifestURL() (string, error) {
	args := m.Called()
	return args.String(0), args.Error(1)
}

func (m *nllbStaticPodsMock) ClaimStaticPod(namespace, name string) (worker.StaticPod, error) {
	args := m.Called(namespace, name)
	return args.Get(0).(worker.StaticPod), args.Error(1)
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
