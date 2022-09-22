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
	"fmt"
	"os"
	"path/filepath"
	"sync"
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
	"github.com/sirupsen/logrus/hooks/test"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/yaml"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"k8s.io/client-go/kubernetes/typed/core/v1/fake"
	k8stesting "k8s.io/client-go/testing"
)

type (
	obj = map[string]any
	arr = []any
)

var (
	noResourcesApplied = func(r resources) (error, error) {
		if len(r) > 0 {
			return nil, errors.New("resources have been applied when they shouldn't")
		}
		return nil, nil
	}
	someResourcesApplied = func(r resources) (error, error) {
		if len(r) < 1 {
			return nil, errors.New("expected some resources to be applied")
		}
		return nil, nil
	}
)

func TestReconciler_Lifecycle(t *testing.T) {
	cleaner := new(mockCleaner)
	clients := testutil.NewFakeClientFactory()
	underTest, err := NewReconciler(
		constant.GetConfig(t.TempDir()),
		&v1beta1.ClusterSpec{
			Network: &v1beta1.Network{
				ClusterDomain: "test.local",
				ServiceCIDR:   "99.99.99.0/24",
			},
		},
		clients, true,
	)
	require.NoError(t, err)
	underTest.cleaner = cleaner

	log := logrus.New()
	log.SetLevel(logrus.DebugLevel)
	underTest.log = log

	t.Run("fails_to_start_without_init", func(t *testing.T) {
		err := underTest.Start(context.TODO())
		require.Error(t, err)
		require.Equal(t, "cannot start: workerconfig.reconcilerCreated", err.Error())
	})

	t.Run("init", func(t *testing.T) {
		cleaner.On("init").Once()
		assert.NoError(t, underTest.Init(context.TODO()))
		cleaner.AssertExpectations(t)
	})

	t.Run("another_init_fails", func(t *testing.T) {
		err := underTest.Init(context.TODO())
		require.Error(t, err)
		assert.Equal(t, "cannot initialize: workerconfig.reconcilerInitialized", err.Error())
		cleaner.AssertExpectations(t)
	})

	mockApplier := installMockApplier(t, underTest)
	mockKubernetesEndpoints(t, clients)

	stopTest := func() func(t *testing.T) {
		var once sync.Once
		return func(t *testing.T) {
			once.Do(func() {
				cleaner.On("stop").Once()
				assert.NoError(t, underTest.Stop())
				cleaner.AssertExpectations(t)
			})
		}
	}()

	t.Run("starts", func(runT *testing.T) {
		mockApplier.expectApply(t, func(r resources) (error, error) { return nil, nil })
		require.NoError(runT, underTest.Start(context.TODO()))
		t.Cleanup(func() { stopTest(t) })
		mockApplier.awaitCalls(t)
		cleaner.AssertExpectations(t)
	})

	t.Run("another_start_fails", func(t *testing.T) {
		err := underTest.Start(context.TODO())
		require.Error(t, err)
		assert.Equal(t, "cannot start: workerconfig.reconcilerStarted", err.Error())
		cleaner.AssertExpectations(t)
	})

	t.Run("reconciles", func(t *testing.T) {
		cleaner.On("reconciled", mock.Anything).Once()

		mockApplier.expectApply(t, func(r resources) (error, error) {
			if len(r) < 1 {
				return nil, errors.New("expected some resources to be applied")
			}
			return nil, nil
		})
		require.NoError(t, underTest.Reconcile(context.TODO(), &v1beta1.ClusterConfig{
			Spec: &v1beta1.ClusterSpec{
				Network: &v1beta1.Network{
					ClusterDomain: "reconcile.local",
				},
				Images: &v1beta1.ClusterImages{
					DefaultPullPolicy: string(corev1.PullNever),
				},
			},
		}))
		mockApplier.awaitCalls(t)
		cleaner.AssertExpectations(t)
	})

	t.Run("stops", func(t *testing.T) {
		stopTest(t)
	})

	t.Run("stop_may_be_called_again", func(t *testing.T) {
		require.NoError(t, underTest.Stop())
		cleaner.AssertExpectations(t)
	})

	t.Run("reinit_fails", func(t *testing.T) {
		err := underTest.Init(context.TODO())
		require.Error(t, err)
		assert.Equal(t, "cannot initialize: workerconfig.reconcilerStopped", err.Error())
		cleaner.AssertExpectations(t)
	})

	t.Run("restart_fails", func(t *testing.T) {
		err := underTest.Start(context.TODO())
		require.Error(t, err)
		assert.Equal(t, "cannot start: workerconfig.reconcilerStopped", err.Error())
		cleaner.AssertExpectations(t)
	})
}

func TestReconciler_ResourceGeneration(t *testing.T) {
	cleaner := new(mockCleaner)
	clients := testutil.NewFakeClientFactory()
	underTest, err := NewReconciler(
		constant.GetConfig(t.TempDir()),
		&v1beta1.ClusterSpec{
			Network: &v1beta1.Network{
				ClusterDomain: "test.local",
				ServiceCIDR:   "99.99.99.0/24",
			},
		},
		clients, true,
	)
	require.NoError(t, err)
	underTest.cleaner = cleaner

	log := logrus.New()
	log.SetLevel(logrus.DebugLevel)
	underTest.log = log

	cleaner.On("init").Once()
	require.NoError(t, underTest.Init(context.TODO()))

	mockKubernetesEndpoints(t, clients)
	mockApplier := installMockApplier(t, underTest)

	require.NoError(t, underTest.Start(context.TODO()))
	t.Cleanup(func() {
		cleaner.On("stop").Once()
		assert.NoError(t, underTest.Stop())
		cleaner.AssertExpectations(t)
	})

	cleaner.On("reconciled", mock.Anything).Once()

	var appliedResources resources
	mockApplier.expectApply(t, noResourcesApplied)
	mockApplier.expectApply(t, func(r resources) (error, error) {
		appliedResources = r
		return nil, nil
	})
	require.NoError(t, underTest.Reconcile(context.TODO(), &v1beta1.ClusterConfig{
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
				NodeLocalLoadBalancer: &v1beta1.NodeLocalLoadBalancer{
					Enabled: pointer.Bool(true),
					Type:    v1beta1.NllbTypeEnvoyProxy,
					EnvoyProxy: &v1beta1.EnvoyProxy{
						Image: &v1beta1.ImageSpec{
							Image: "envoy", Version: "test",
						},
					},
				},
			},
			Images: &v1beta1.ClusterImages{
				DefaultPullPolicy: string(corev1.PullNever),
			},
		},
	}))
	mockApplier.awaitCalls(t)

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

	assert.Len(t, appliedResources, len(configMaps)+4)

	for name, mod := range configMaps {
		t.Run(name, func(t *testing.T) {
			kubelet := requireKubelet(t, appliedResources, name)
			expected := makeKubeletConfig(t, func(expected obj) { mod(t, expected) })
			assert.JSONEq(t, expected, kubelet)
		})
	}

	const rbacName = "system:bootstrappers:worker-config"

	t.Run("Role", func(t *testing.T) {
		role := find(t, "Expected to find a Role named "+rbacName,
			appliedResources, func(resource *u.Unstructured) bool {
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
			appliedResources, func(resource *u.Unstructured) bool {
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
	underTest, err := NewReconciler(
		constant.GetConfig(t.TempDir()),
		&v1beta1.ClusterSpec{
			Network: &v1beta1.Network{
				ClusterDomain: "test.local",
				ServiceCIDR:   "99.99.99.0/24",
			},
		},
		clients, true,
	)
	require.NoError(t, err)
	underTest.cleaner = cleaner

	log := logrus.New()
	log.SetLevel(logrus.DebugLevel)
	underTest.log = log
	logs := test.NewLocal(log)

	// clients.Client.CoreV1().(*fake.FakeCoreV1).PrependReactor("list", "endpoints", func(k8stesting.Action) (bool, runtime.Object, error) {
	// 	// Otherwise this would replace the mocked API servers again
	// 	return true, nil, errors.New("disabled for testing")
	// })

	cleaner.On("init").Once()
	require.NoError(t, underTest.Init(context.TODO()))

	mockKubernetesEndpoints(t, clients)
	mockApplier := installMockApplier(t, underTest)

	mockApplier.expectApply(t, noResourcesApplied)
	require.NoError(t, underTest.Start(context.TODO()))
	t.Cleanup(func() {
		cleaner.On("stop")
		assert.NoError(t, underTest.Stop())
		cleaner.AssertExpectations(t)
	})
	mockApplier.awaitCalls(t)

	cleaner.On("reconciled", mock.Anything)

	expectApply := func(t *testing.T) {
		t.Helper()
		mockApplier.expectApply(t, someResourcesApplied)
		assert.NoError(t, underTest.Reconcile(context.TODO(), cluster))
		mockApplier.awaitCalls(t)
	}

	expectCached := func(t *testing.T) {
		t.Helper()
		mockApplier.expectApply(t, noResourcesApplied)
		assert.NoError(t, underTest.Reconcile(context.TODO(), cluster))
		mockApplier.awaitCalls(t)
	}

	expectApplyButFail := func(t *testing.T) {
		t.Helper()
		mockApplier.expectApply(t, func(r resources) (error, error) {
			if _, err := someResourcesApplied(r); err != nil {
				return nil, err
			}
			return assert.AnError, nil
		})
		assert.NoError(t, underTest.Reconcile(context.TODO(), cluster))
		mockApplier.awaitCalls(t)
		assert.NoError(t, retry.Do(
			func() error {
				for _, entry := range logs.AllEntries() {
					if entry.Level == logrus.ErrorLevel {
						if err, ok := entry.Data[logrus.ErrorKey].(error); ok && err == assert.AnError {
							return nil
						}
					}
				}
				return errors.New("no error logged")
			},
			retry.LastErrorOnly(true),
			retry.MaxDelay(500*time.Millisecond),
		), "Expected the mocked error to be logged")
	}

	// Set some value that affects worker configs.
	cluster.Spec.Images.DefaultPullPolicy = string(corev1.PullNever)
	t.Run("first_time_apply", expectApply)
	t.Run("second_time_cached", expectCached)

	// Change that value, so that configs need to be reapplied.
	cluster.Spec.Images.DefaultPullPolicy = string(corev1.PullAlways)
	t.Run("third_time_apply_fails", expectApplyButFail)

	// After an error, expect a reapplication in any case.
	t.Run("fourth_time_apply", expectApply)

	// Even if the last successfully applied config is restored, expect it to be applied after a failure.
	cluster.Spec.Images.DefaultPullPolicy = string(corev1.PullNever)
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

	t.Run("leaves_unrelated_files_alone", func(t *testing.T) {
		underTest, err := NewReconciler(
			constant.GetConfig(t.TempDir()),
			&v1beta1.ClusterSpec{
				Network: &v1beta1.Network{
					ClusterDomain: "test.local",
					ServiceCIDR:   "99.99.99.0/24",
				},
			},
			testutil.NewFakeClientFactory(), true,
		)
		require.NoError(t, err)

		assert.NoError(t, underTest.Init(context.TODO()))

		assert.NoFileExists(t, file, "Expected the deprecated file to be deleted.")
		assert.FileExists(t, unrelatedFile, "Expected the unrelated file to be untouched.")
	})

	require.NoError(t, os.Remove(unrelatedFile))

	t.Run("prunes_empty_folder", func(t *testing.T) {
		underTest, err := NewReconciler(
			constant.GetConfig(t.TempDir()),
			&v1beta1.ClusterSpec{
				Network: &v1beta1.Network{
					ClusterDomain: "test.local",
					ServiceCIDR:   "99.99.99.0/24",
				},
			},
			testutil.NewFakeClientFactory(), true,
		)
		require.NoError(t, err)

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
				"99.99.99.99",
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

// type mockApplyState struct {
// 	ignoreCached bool
// 	fn           func(resources) error
// }

// type mockApply struct {
// 	// lastApplied atomic.Pointer[resources]
// 	// timesCalled atomic.Int32
// 	// err         atomic.Pointer[error]

// 	applier atomic.Pointer[mockApplyState]
// }

// func (m *mockApply) expect(t *testing.T, ignoreCached bool, outcome error) func() resources {
// 	applied := make(chan resources, 1)
// 	fn := func(r resources) error {
// 		defer close(applied)
// 		applied <- r
// 		return outcome
// 	}

// 	state := &mockApplyState{ignoreCached, fn}
// 	if !m.applier.CompareAndSwap(nil, state) {
// 		require.FailNow(t, "attempt to expect an apply function before another one has been consumed")
// 	}

// 	var called atomic.Bool
// 	return func() resources {
// 		if !called.CompareAndSwap(false, true) {
// 			require.FailNow(t, "expectation already awaited")
// 		}

// 		select {
// 		case applied := <-applied:
// 			return applied
// 		case <-time.After(10 * time.Second):
// 			for {
// 				if !m.applier.CompareAndSwap(state, nil) {
// 					if curState := m.applier.Load(); curState != state {
// 						assert.Fail(t, "failed to clear timed-out state", "%p vs %p", state, curState)
// 						break
// 					}
// 				}
// 			}
// 			require.FailNow(t, "timed out while waiting for apply")
// 			return nil // satisfy compiler
// 		}
// 	}
// }

// func (m *mockApply) reset() {
// 	m.lastApplied.Store(nil)
// 	m.timesCalled.Store(0)
// 	m.err.Store(nil)
// }

// func (m *mockApply) getLastApplied() resources {
// 	applied := m.lastApplied.Load()
// 	if applied == nil {
// 		return nil
// 	}
// 	return *applied
// }

type mockApplier struct {
	callsPtr    atomic.Pointer[mockApplierCalls]
	applyCalled <-chan mockApplyCalled
}

type mockApplierCalls struct {
	callbacks  []mockApplierCallbackInfo
	nextOffset uint
}

type mockApplierCallbackInfo struct {
	testName string
	callback mockApplierCallback
}

type mockApplierCallback = func(resources) (error, error)

type mockApplyCalled struct {
	offset uint
	err    error
}

func (m *mockApplier) expectApply(t *testing.T, callback mockApplierCallback) {
	for {
		state := m.callsPtr.Load()
		newState := *state
		newState.callbacks = append(newState.callbacks, mockApplierCallbackInfo{
			testName: t.Name(), callback: callback,
		})
		if m.callsPtr.CompareAndSwap(state, &newState) {
			return
		}
	}
}

func (m *mockApplier) awaitCalls(t *testing.T) {
	wantedOffset, wait := func() (uint, bool) {
		state := m.callsPtr.Load()
		maxOffset := uint(len(state.callbacks))
		if state.nextOffset < maxOffset {
			return maxOffset - 1, true
		}
		return 0, false
	}()

	if !wait {
		return
	}

	for timeout := time.After(10 * time.Second); ; {
		select {
		case lastCalled, ok := <-m.applyCalled:
			if !ok {
				require.Fail(t, "channel closed unexpectedly")
			}
			t.Logf("Seen call to apply(): %+#v", lastCalled)
			require.NoError(t, lastCalled.err, "Call #%d to apply failed verification", lastCalled.offset+1)
			if lastCalled.offset >= wantedOffset {
				return
			}

		case <-timeout:
			require.Fail(t,
				"timed out while waiting for call to apply()",
				"currently called %d times, waiting for call %d",
				m.callsPtr.Load().nextOffset, wantedOffset+1,
			)
		}
	}
}

func installMockApplier(t *testing.T, underTest *Reconciler) *mockApplier {
	applyCalled := make(chan mockApplyCalled, 3)
	mockApplier := mockApplier{applyCalled: applyCalled}
	mockApplier.callsPtr.Store(&mockApplierCalls{})

	underTest.mu.Lock()
	defer underTest.mu.Unlock()

	state := underTest.load()
	initialized, ok := state.(reconcilerInitialized)
	require.True(t, ok, "unexpected state: %T", state)
	require.NotNil(t, initialized.apply)
	t.Cleanup(func() {
		close(applyCalled)
	})

	initialized.apply = func(ctx context.Context, r resources) error {
		for {
			state := mockApplier.callsPtr.Load()
			offset := state.nextOffset
			if offset >= uint(len(state.callbacks)) {
				err := errors.New("unmocked call to apply")
				applyCalled <- mockApplyCalled{offset, err}
				return err
			}
			newState := *state
			newState.nextOffset = offset + 1
			if mockApplier.callsPtr.CompareAndSwap(state, &newState) {
				info := state.callbacks[offset]
				ret, err := info.callback(r)
				if err != nil {
					applyCalled <- mockApplyCalled{offset, fmt.Errorf("%s: %w", info.testName, err)}
					return err
				}
				applyCalled <- mockApplyCalled{offset, nil}
				return ret
			}
		}
	}

	// initialized.apply = func(_ context.Context, resources resources) error {
	// 	mockApply.timesCalled.Add(1)
	// 	mockApply.lastApplied.Store(&resources)
	// 	if errPtr := mockApply.err.Load(); errPtr != nil {
	// 		return *errPtr
	// 	}
	// 	return nil
	// }

	underTest.store(initialized)
	return &mockApplier
}

func mockKubernetesEndpoints(t *testing.T, clients testutil.FakeClientFactory) {
	client, err := clients.GetClient()
	require.NoError(t, err)

	ep := corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{ResourceVersion: t.Name()},
		Subsets: []corev1.EndpointSubset{{
			Addresses: []corev1.EndpointAddress{
				{IP: "127.10.10.1"},
			},
			Ports: []corev1.EndpointPort{
				{Name: "https", Port: 6443, Protocol: corev1.ProtocolTCP},
			},
		}},
	}

	epList := corev1.EndpointsList{
		ListMeta: metav1.ListMeta{ResourceVersion: t.Name()},
		Items:    []corev1.Endpoints{ep},
	}

	_, err = client.CoreV1().Endpoints("default").Create(context.TODO(), ep.DeepCopy(), metav1.CreateOptions{})
	require.NoError(t, err)

	clients.Client.CoreV1().(*fake.FakeCoreV1).PrependReactor("list", "endpoints", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, epList.DeepCopy(), nil
	})
}

type mockCleaner struct{ mock.Mock }

func (m *mockCleaner) init()                          { m.Called() }
func (m *mockCleaner) reconciled(ctx context.Context) { m.Called(ctx) }
func (m *mockCleaner) stop()                          { m.Called() }
