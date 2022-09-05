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

package workerconfig

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/avast/retry-go"
	"github.com/k0sproject/k0s/internal/testutil"
	"github.com/k0sproject/k0s/pkg/apis/k0s.k0sproject.io/v1beta1"
	"github.com/k0sproject/k0s/pkg/constant"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	u "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	"github.com/sirupsen/logrus"
	"sigs.k8s.io/yaml"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/kubernetes/typed/core/v1/fake"
	k8stesting "k8s.io/client-go/testing"
)

type obj = map[string]any
type arr = []any

func TestReconciler_ResourceGeneration(t *testing.T) {
	cleaner := new(mockCleaner)
	clients := testutil.NewFakeClientFactory()
	underTest := NewReconciler(constant.GetConfig(t.TempDir()), clients)
	underTest.cleaner = cleaner

	client, err := clients.GetClient()
	require.NoError(t, err)

	kubernetesEndpoints := &corev1.EndpointsList{
		ListMeta: metav1.ListMeta{ResourceVersion: t.Name()},
		Items: []corev1.Endpoints{{
			ObjectMeta: metav1.ObjectMeta{ResourceVersion: t.Name()},
			Subsets: []corev1.EndpointSubset{{
				Addresses: []corev1.EndpointAddress{
					{IP: "127.10.10.1"},
				},
				Ports: []corev1.EndpointPort{
					{Name: "https", Port: 6443, Protocol: corev1.ProtocolTCP},
				},
			}},
		}},
	}

	_, err = client.CoreV1().Endpoints("default").Create(context.TODO(), kubernetesEndpoints.Items[0].DeepCopy(), metav1.CreateOptions{})
	require.NoError(t, err)
	clients.Client.CoreV1().(*fake.FakeCoreV1).PrependReactor("list", "endpoints", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, kubernetesEndpoints.DeepCopy(), nil
	})

	require.NoError(t, underTest.Start(context.TODO()))
	t.Cleanup(func() {
		cleaner.On("stop").Once()
		assert.NoError(t, underTest.Stop())
		cleaner.AssertExpectations(t)
	})

	lastApplied, callsToApply := mockDoApply(t, underTest, nil)

	cleaner.On("reconciled", mock.Anything).Once()

	require.NoError(t, retry.Do(
		func() error {
			err := underTest.Reconcile(context.TODO(), &v1beta1.ClusterConfig{
				Spec: &v1beta1.ClusterSpec{
					WorkerProfiles: v1beta1.WorkerProfiles{{
						Name:   "profile_XXX",
						Config: []byte(`{"authentication": {"anonymous": {"enabled": true}}}`),
					}, {
						Name:   "profile_YYY",
						Config: []byte(`{"authentication": {"webhook": {"cacheTTL": "15s"}}}`),
					}},
					Network: &v1beta1.Network{
						ClusterDomain: "test.local",
						ServiceCIDR:   "10.254.254.0/24",
					},
					Images: &v1beta1.ClusterImages{
						EnvoyProxy:        v1beta1.ImageSpec{Image: "envoy", Version: "test"},
						DefaultPullPolicy: string(corev1.PullNever),
					},
				},
			})
			if err != nil {
				return retry.Unrecoverable(err)
			}

			if callsToApply() < 1 {
				return errors.New("not yet applied")
			}

			return nil
		},
		retry.MaxDelay(500*time.Millisecond),
		retry.LastErrorOnly(true),
	))

	configMaps := map[string]func(t *testing.T, expected obj){
		"worker-config-default-1.25": func(t *testing.T, expected obj) {
			require.NoError(t, u.SetNestedField(expected, true, "cgroupsPerQOS"))
		},

		"worker-config-default-windows-1.25": func(t *testing.T, expected obj) {
			require.NoError(t, u.SetNestedField(expected, false, "cgroupsPerQOS"))
		},

		"worker-config-profile_XXX-1.25": func(t *testing.T, expected obj) {
			require.NoError(t, u.SetNestedField(expected, true, "authentication", "anonymous", "enabled"))
		},

		"worker-config-profile_YYY-1.25": func(t *testing.T, expected obj) {
			require.NoError(t, u.SetNestedField(expected, "15s", "authentication", "webhook", "cacheTTL"))
		},
	}

	assert.Equal(t, uint(1), callsToApply(), "Expected a single call to doApply")
	assert.Len(t, lastApplied(), len(configMaps)+4)

	for name, mod := range configMaps {
		t.Run(name, func(t *testing.T) {
			kubelet := requireKubelet(t, lastApplied(), name)
			expected := makeKubeletConfig(t, func(expected obj) { mod(t, expected) })
			assert.JSONEq(t, expected, kubelet)
		})
	}

	const rbacName = "system:bootstrappers:worker-config"

	t.Run("Role", func(t *testing.T) {
		role := find(t, "Expected to find a Role named "+rbacName,
			lastApplied(), func(resource *u.Unstructured) bool {
				return resource.GetKind() == "Role" && resource.GetName() == rbacName
			},
		)

		rules, ok, err := u.NestedSlice(role.Object, "rules")
		require.NoError(t, err)
		require.True(t, ok, "No rules field")
		require.Len(t, rules, 1, "Expected a single rule")

		rule, ok := rules[0].(obj)
		require.True(t, ok, "Invalid rule")

		resourceNames, ok, err := u.NestedStringSlice(rule, "resourceNames")
		require.NoError(t, err)
		require.True(t, ok, "No resourceNames field")

		assert.Len(t, resourceNames, len(configMaps))
		for expected := range configMaps {
			assert.Contains(t, resourceNames, expected)
		}
	})

	t.Run("RoleBinding", func(t *testing.T) {
		binding := find(t, "Expected to find a RoleBinding named "+rbacName,
			lastApplied(), func(resource *u.Unstructured) bool {
				return resource.GetKind() == "RoleBinding" && resource.GetName() == rbacName
			},
		)

		roleRef, ok, err := u.NestedMap(binding.Object, "roleRef")
		if assert.NoError(t, err) && assert.True(t, ok, "No roleRef field") {
			expected := obj{
				"apiGroup": "rbac.authorization.k8s.io",
				"kind":     "Role",
				"name":     rbacName,
			}

			assert.Equal(t, expected, roleRef)
		}

		subjects, ok, err := u.NestedSlice(binding.Object, "subjects")
		if assert.NoError(t, err) && assert.True(t, ok, "No subjects field") {
			expected := arr{obj{
				"apiGroup": "rbac.authorization.k8s.io",
				"kind":     "Group",
				"name":     "system:bootstrappers",
			}, obj{
				"apiGroup": "rbac.authorization.k8s.io",
				"kind":     "Group",
				"name":     "system:nodes",
			}}

			assert.Equal(t, expected, subjects)
		}
	})
}

func TestReconciler_ReconcilesOnChangesOnly(t *testing.T) {
	cluster := v1beta1.DefaultClusterConfig(nil)
	cleaner := new(mockCleaner)
	clients := testutil.NewFakeClientFactory()
	underTest := NewReconciler(constant.GetConfig(t.TempDir()), clients)
	underTest.cleaner = cleaner

	log := logrus.New()
	log.SetLevel(logrus.DebugLevel)
	underTest.log = log

	clients.Client.CoreV1().(*fake.FakeCoreV1).PrependReactor("list", "endpoints", func(k8stesting.Action) (bool, runtime.Object, error) {
		// Otherwise this would replace the mocked API servers again
		return true, nil, errors.New("disabled for testing")
	})

	require.NoError(t, underTest.Start(context.TODO()))
	t.Cleanup(func() {
		cleaner.On("stop")
		assert.NoError(t, underTest.Stop())
		cleaner.AssertExpectations(t)
	})

	cleaner.On("reconciled", mock.Anything)

	func() { // Inject API servers
		state, ok := underTest.load().(*reconcilerStarted)
		require.True(t, ok)
		state.mu.Lock()
		defer state.mu.Unlock()
		underTest.load().(*reconcilerStarted).snapshot.apiServers = apiServers{{"127.0.0.1", 6443}}
	}()

	expectApply := func(t *testing.T) {
		t.Helper()
		applied, _ := mockDoApply(t, underTest, nil)

		assert.NoError(t, underTest.Reconcile(context.TODO(), cluster))
		assert.NotEmpty(t, applied(), "Expected some resources to be applied")
	}

	expectCached := func(t *testing.T) {
		t.Helper()
		_, calls := mockDoApply(t, underTest, nil)

		assert.NoError(t, underTest.Reconcile(context.TODO(), cluster))
		assert.Zero(t, calls(), "Resources have been applied when they shouldn't.")
	}

	expectApplyButFail := func(t *testing.T) {
		t.Helper()
		mockDoApply(t, underTest, assert.AnError)

		err := underTest.Reconcile(context.TODO(), cluster)
		if assert.Error(t, err) {
			assert.True(t, errors.Is(err, assert.AnError), "Expected to see the doApply error here.")
		}
	}

	// Set some value that affects worker configs.
	cluster.Spec.Network.ClusterDomain = "one.local"
	t.Run("first_time_apply", expectApply)
	t.Run("second_time_cached", expectCached)

	// Change that value, so that configs need to be reapplied.
	cluster.Spec.Network.ClusterDomain = "another.local"
	t.Run("third_time_apply_fails", expectApplyButFail)

	// After an error, expect a reapplication in any case.
	t.Run("fourth_time_apply_fails", expectApplyButFail)

	// Even if the last successfully applied config is restored, expect it to be applied after a failure.
	cluster.Spec.Network.ClusterDomain = "one.local"
	t.Run("fifth_time_apply", expectApply)
	t.Run("sixth_time_cached", expectCached)
}

func TestReconciler_Cleaner_CleansUpManifestsOnInit(t *testing.T) {
	k0sVars := constant.GetConfig(t.TempDir())
	folder := filepath.Join(k0sVars.ManifestsDir, "kubelet")
	file := filepath.Join(folder, "kubelet-config.yaml")
	unrelatedFile := filepath.Join(folder, "unrelated")
	require.NoError(t, os.MkdirAll(folder, 0755))
	require.NoError(t, os.WriteFile(file, []byte("foo"), 0644))
	require.NoError(t, os.WriteFile(unrelatedFile, []byte("foo"), 0644))

	underTest := NewReconciler(k0sVars, testutil.NewFakeClientFactory())

	t.Run("leaves_unrelated_files_alone", func(t *testing.T) {
		assert.NoError(t, underTest.Init(context.TODO()))

		assert.NoFileExists(t, file, "Expected the deprecated file to be deleted.")
		assert.FileExists(t, unrelatedFile, "Expected the unrelated file to be untouched.")
	})

	require.NoError(t, os.Remove(unrelatedFile))

	t.Run("prunes_empty_folder", func(t *testing.T) {
		assert.NoError(t, underTest.Init(context.TODO()))

		assert.NoDirExists(t, folder, "Expected the empty deprecated folder to be deleted.")
		assert.DirExists(t, k0sVars.ManifestsDir, "Expected the manifests folder to be untouched.")
	})
}

func requireKubelet(t *testing.T, resources []*u.Unstructured, name string) string {
	configMap := find(t, "No ConfigMap found with name "+name,
		resources, func(resource *u.Unstructured) bool {
			return resource.GetKind() == "ConfigMap" && resource.GetName() == name
		},
	)
	kubeletConfigYAML, ok, err := u.NestedString(configMap.Object, "data", "kubeletConfiguration")
	require.NoError(t, err)
	require.True(t, ok, "No data.kubeletConfiguration field")
	kubeletConfigJSON, err := yaml.YAMLToJSONStrict([]byte(kubeletConfigYAML))
	require.NoError(t, err)
	return string(kubeletConfigJSON)
}

func find[T any](t *testing.T, failureMessage string, items []T, filter func(T) bool) (item T) {
	for _, item := range items {
		if filter(item) {
			return item
		}
	}

	require.Fail(t, failureMessage)
	return item
}

func makeKubeletConfig(t *testing.T, mods ...func(obj)) string {
	kubelet := u.Unstructured{
		Object: obj{
			"apiVersion": "kubelet.config.k8s.io/v1beta1",
			"authentication": obj{
				"anonymous": obj{},
				"webhook": obj{
					"cacheTTL": "0s",
				},
				"x509": obj{},
			},
			"authorization": obj{
				"webhook": obj{
					"cacheAuthorizedTTL":   "0s",
					"cacheUnauthorizedTTL": "0s",
				},
			},
			"clusterDNS": arr{
				"10.254.254.10",
			},
			"clusterDomain":                    "test.local",
			"cpuManagerReconcilePeriod":        "0s",
			"eventRecordQPS":                   0,
			"evictionPressureTransitionPeriod": "0s",
			"failSwapOn":                       false,
			"fileCheckFrequency":               "0s",
			"httpCheckFrequency":               "0s",
			"imageMinimumGCAge":                "0s",
			"kind":                             "KubeletConfiguration",
			"logging": obj{
				"flushFrequency": 0,
				"options": obj{
					"json": obj{
						"infoBufferSize": "0",
					},
				},
				"verbosity": 0,
			},
			"memorySwap":                      obj{},
			"nodeStatusReportFrequency":       "0s",
			"nodeStatusUpdateFrequency":       "0s",
			"rotateCertificates":              true,
			"runtimeRequestTimeout":           "0s",
			"serverTLSBootstrap":              true,
			"shutdownGracePeriod":             "0s",
			"shutdownGracePeriodCriticalPods": "0s",
			"streamingConnectionIdleTimeout":  "0s",
			"syncFrequency":                   "0s",
			"tlsCipherSuites": arr{
				"TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256",
				"TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384",
				"TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305",
				"TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256",
				"TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384",
				"TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305",
				"TLS_RSA_WITH_AES_128_GCM_SHA256",
				"TLS_RSA_WITH_AES_256_GCM_SHA384",
			},
			"volumeStatsAggPeriod": "0s",
		},
	}

	for _, mod := range mods {
		mod(kubelet.Object)
	}

	json, err := kubelet.MarshalJSON()
	require.NoError(t, err)
	return string(json)
}

func mockDoApply(t *testing.T, underTest *Reconciler, err error) (func() resources, func() uint) {
	var applied atomic.Pointer[resources]
	var called atomic.Int32

	state, ok := underTest.load().(*reconcilerStarted)
	require.True(t, ok)
	state.mu.Lock()
	defer state.mu.Unlock()
	require.NotNil(t, state.doApply)
	state.doApply = func(_ context.Context, resources resources) error {
		called.Add(1)
		applied.Store(&resources)
		return err
	}

	appliedFn := func() resources {
		applied := applied.Load()
		if applied == nil {
			return nil
		}
		return *applied
	}

	calledFn := func() uint { return uint(called.Load()) }

	return appliedFn, calledFn
}

type mockCleaner struct{ mock.Mock }

func (m *mockCleaner) init()                          { m.Called() }
func (m *mockCleaner) reconciled(ctx context.Context) { m.Called(ctx) }
func (m *mockCleaner) stop()                          { m.Called() }
