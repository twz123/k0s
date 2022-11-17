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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/k0sproject/k0s/internal/testutil"
	k0sv1beta1 "github.com/k0sproject/k0s/pkg/apis/k0s.k0sproject.io/v1beta1"
	"github.com/k0sproject/k0s/pkg/component/controller/leaderelector"
	"github.com/k0sproject/k0s/pkg/constant"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	kubeletv1beta1 "k8s.io/kubelet/config/v1beta1"
	"k8s.io/utils/pointer"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"
)

type (
	obj           = map[string]any
	arr           = []any
	kubeletConfig = kubeletv1beta1.KubeletConfiguration
)

func TestReconciler_Lifecycle(t *testing.T) {
	cleaner := newMockCleaner()
	underTest, err := NewReconciler(
		constant.GetConfig(t.TempDir()),
		&k0sv1beta1.ClusterSpec{
			API: &k0sv1beta1.APISpec{},
			Network: &k0sv1beta1.Network{
				ClusterDomain: "test.local",
				ServiceCIDR:   "99.99.99.0/24",
			},
		},
		testutil.NewFakeClientFactory(),
		&leaderelector.Dummy{Leader: true},
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
		assert.NoError(t, underTest.Init(context.TODO()))
		assert.Equal(t, "init", cleaner.state.Load())
	})

	t.Run("another_init_fails", func(t *testing.T) {
		err := underTest.Init(context.TODO())
		require.Error(t, err)
		assert.Equal(t, "cannot initialize: workerconfig.reconcilerInitialized", err.Error())
		assert.Equal(t, "init", cleaner.state.Load())
	})

	mockApplier := installMockApplier(t, underTest)

	stopTest := func() func(t *testing.T) {
		var once sync.Once
		return func(t *testing.T) {
			once.Do(func() {
				assert.NoError(t, underTest.Stop())
				assert.Equal(t, "stop", cleaner.state.Load())
			})
		}
	}()

	t.Run("starts", func(runT *testing.T) {
		require.NoError(runT, underTest.Start(context.TODO()))
		t.Cleanup(func() { stopTest(t) })
		assert.Equal(t, "init", cleaner.state.Load())
	})

	t.Run("another_start_fails", func(t *testing.T) {
		err := underTest.Start(context.TODO())
		require.Error(t, err)
		assert.Equal(t, "cannot start: workerconfig.reconcilerStarted", err.Error())
		assert.Equal(t, "init", cleaner.state.Load())
	})

	t.Run("reconciles", func(t *testing.T) {
		applied := mockApplier.expectApply(t, nil)
		require.NoError(t, underTest.Reconcile(context.TODO(), &k0sv1beta1.ClusterConfig{
			Spec: &k0sv1beta1.ClusterSpec{
				Network: &k0sv1beta1.Network{
					ClusterDomain: "reconcile.local",
				},
				Images: &k0sv1beta1.ClusterImages{
					DefaultPullPolicy: string(corev1.PullNever),
				},
			},
		}))
		assert.NotEmpty(t, applied(), "Expected some resources to be applied")
		cleaner.awaitState(t, "reconciled")
	})

	t.Run("stops", func(t *testing.T) {
		stopTest(t)
	})

	t.Run("stop_may_be_called_again", func(t *testing.T) {
		require.NoError(t, underTest.Stop())
		cleaner.assertState(t, "stop")
	})

	t.Run("reinit_fails", func(t *testing.T) {
		err := underTest.Init(context.TODO())
		require.Error(t, err)
		assert.Equal(t, "cannot initialize: workerconfig.reconcilerStopped", err.Error())
		cleaner.assertState(t, "stop")
	})

	t.Run("restart_fails", func(t *testing.T) {
		err := underTest.Start(context.TODO())
		require.Error(t, err)
		assert.Equal(t, "cannot start: workerconfig.reconcilerStopped", err.Error())
		cleaner.assertState(t, "stop")
	})
}

func TestReconciler_ResourceGeneration(t *testing.T) {
	cleaner := newMockCleaner()
	underTest, err := NewReconciler(
		constant.GetConfig(t.TempDir()),
		&k0sv1beta1.ClusterSpec{
			API: &k0sv1beta1.APISpec{},
			Network: &k0sv1beta1.Network{
				ClusterDomain: "test.local",
				ServiceCIDR:   "99.99.99.0/24",
			},
		},
		testutil.NewFakeClientFactory(),
		&leaderelector.Dummy{Leader: true},
	)
	require.NoError(t, err)
	underTest.cleaner = cleaner

	log := logrus.New()
	log.SetLevel(logrus.DebugLevel)
	underTest.log = log

	require.NoError(t, underTest.Init(context.TODO()))

	mockApplier := installMockApplier(t, underTest)

	require.NoError(t, underTest.Start(context.TODO()))
	t.Cleanup(func() {
		assert.NoError(t, underTest.Stop())
	})

	applied := mockApplier.expectApply(t, nil)

	require.NoError(t, underTest.Reconcile(context.TODO(), &k0sv1beta1.ClusterConfig{
		Spec: &k0sv1beta1.ClusterSpec{
			WorkerProfiles: k0sv1beta1.WorkerProfiles{{
				Name:   "profile_XXX",
				Config: []byte(`{"authentication": {"anonymous": {"enabled": true}}}`),
			}, {
				Name:   "profile_YYY",
				Config: []byte(`{"authentication": {"webhook": {"cacheTTL": "15s"}}}`),
			}},
			Network: &k0sv1beta1.Network{
				ClusterDomain: "test.local",
				ServiceCIDR:   "10.254.254.0/24",
			},
			Images: &k0sv1beta1.ClusterImages{
				DefaultPullPolicy: string(corev1.PullNever),
			},
		},
	}))

	configMaps := map[string]func(t *testing.T, expected *kubeletConfig){
		"worker-config-default-1.25": func(t *testing.T, expected *kubeletConfig) {
			expected.CgroupsPerQOS = pointer.Bool(true)
		},

		"worker-config-default-windows-1.25": func(t *testing.T, expected *kubeletConfig) {
			expected.CgroupsPerQOS = pointer.Bool(false)
		},

		"worker-config-profile_XXX-1.25": func(t *testing.T, expected *kubeletConfig) {
			expected.Authentication.Anonymous.Enabled = pointer.Bool(true)
		},

		"worker-config-profile_YYY-1.25": func(t *testing.T, expected *kubeletConfig) {
			expected.Authentication.Webhook.CacheTTL = metav1.Duration{Duration: 15 * time.Second}
		},
	}

	appliedResources := applied()
	assert.Len(t, appliedResources, len(configMaps)+4)

	for name, mod := range configMaps {
		t.Run(name, func(t *testing.T) {
			kubelet := requireKubelet(t, appliedResources, name)
			expected := makeKubeletConfig(t, func(expected *kubeletConfig) { mod(t, expected) })
			assert.JSONEq(t, expected, kubelet)
		})
	}

	const rbacName = "system:bootstrappers:worker-config"

	t.Run("Role", func(t *testing.T) {
		role := find(t, "Expected to find a Role named "+rbacName,
			appliedResources, func(resource *unstructured.Unstructured) bool {
				return resource.GetKind() == "Role" && resource.GetName() == rbacName
			},
		)

		rules, ok, err := unstructured.NestedSlice(role.Object, "rules")
		require.NoError(t, err)
		require.True(t, ok, "No rules field")
		require.Len(t, rules, 1, "Expected a single rule")

		rule, ok := rules[0].(obj)
		require.True(t, ok, "Invalid rule")

		resourceNames, ok, err := unstructured.NestedStringSlice(rule, "resourceNames")
		require.NoError(t, err)
		require.True(t, ok, "No resourceNames field")

		assert.Len(t, resourceNames, len(configMaps))
		for expected := range configMaps {
			assert.Contains(t, resourceNames, expected)
		}
	})

	t.Run("RoleBinding", func(t *testing.T) {
		binding := find(t, "Expected to find a RoleBinding named "+rbacName,
			appliedResources, func(resource *unstructured.Unstructured) bool {
				return resource.GetKind() == "RoleBinding" && resource.GetName() == rbacName
			},
		)

		roleRef, ok, err := unstructured.NestedMap(binding.Object, "roleRef")
		if assert.NoError(t, err) && assert.True(t, ok, "No roleRef field") {
			expected := obj{
				"apiGroup": "rbac.authorization.k8s.io",
				"kind":     "Role",
				"name":     rbacName,
			}

			assert.Equal(t, expected, roleRef)
		}

		subjects, ok, err := unstructured.NestedSlice(binding.Object, "subjects")
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
	cluster := k0sv1beta1.DefaultClusterConfig(nil)
	cleaner := newMockCleaner()
	underTest, err := NewReconciler(
		constant.GetConfig(t.TempDir()),
		&k0sv1beta1.ClusterSpec{
			API: &k0sv1beta1.APISpec{},
			Network: &k0sv1beta1.Network{
				ClusterDomain: "test.local",
				ServiceCIDR:   "99.99.99.0/24",
			},
		},
		testutil.NewFakeClientFactory(),
		&leaderelector.Dummy{Leader: true},
	)
	require.NoError(t, err)
	underTest.cleaner = cleaner

	log := logrus.New()
	log.SetLevel(logrus.DebugLevel)
	underTest.log = log

	require.NoError(t, underTest.Init(context.TODO()))

	mockApplier := installMockApplier(t, underTest)

	require.NoError(t, underTest.Start(context.TODO()))
	t.Cleanup(func() {
		assert.NoError(t, underTest.Stop())
	})

	expectApply := func(t *testing.T) {
		t.Helper()
		applied := mockApplier.expectApply(t, nil)
		assert.NoError(t, underTest.Reconcile(context.TODO(), cluster))
		appliedResources := applied()
		assert.NotEmpty(t, applied, "Expected some resources to be applied")

		for _, r := range appliedResources {
			pp, found, err := unstructured.NestedString(r.Object, "data", "defaultImagePullPolicy")
			assert.NoError(t, err)
			if found {
				t.Logf("%s/%s: %s", r.GetKind(), r.GetName(), pp)
			}
		}

		cleaner.awaitState(t, "reconciled")
	}

	expectCached := func(t *testing.T) {
		t.Helper()
		assert.NoError(t, underTest.Reconcile(context.TODO(), cluster))
	}

	expectApplyButFail := func(t *testing.T) {
		t.Helper()
		applied := mockApplier.expectApply(t, assert.AnError)
		assert.ErrorIs(t, underTest.Reconcile(context.TODO(), cluster), assert.AnError)
		assert.NotEmpty(t, applied(), "Expected some resources to be applied")
	}

	// Set some value that affects worker configs.
	cluster.Spec.WorkerProfiles = k0sv1beta1.WorkerProfiles{{Name: "foo", Config: json.RawMessage(`{"nodeLeaseDurationSeconds": 1}`)}}
	t.Run("first_time_apply", expectApply)
	t.Run("second_time_cached", expectCached)

	// Change that value, so that configs need to be reapplied.
	cluster.Spec.WorkerProfiles = k0sv1beta1.WorkerProfiles{{Name: "foo", Config: json.RawMessage(`{"nodeLeaseDurationSeconds": 2}`)}}
	t.Run("third_time_apply_fails", expectApplyButFail)

	// After an error, expect a reapplication in any case.
	t.Run("fourth_time_apply", expectApply)

	// Even if the last successfully applied config is restored, expect it to be applied after a failure.
	cluster.Spec.WorkerProfiles = k0sv1beta1.WorkerProfiles{{Name: "foo", Config: json.RawMessage(`{"nodeLeaseDurationSeconds": 1}`)}}
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
			k0sVars,
			&k0sv1beta1.ClusterSpec{
				API: &k0sv1beta1.APISpec{},
				Network: &k0sv1beta1.Network{
					ClusterDomain: "test.local",
					ServiceCIDR:   "99.99.99.0/24",
				},
			},
			testutil.NewFakeClientFactory(),
			&leaderelector.Dummy{Leader: true},
		)
		require.NoError(t, err)

		assert.NoError(t, underTest.Init(context.TODO()))

		assert.NoFileExists(t, file, "Expected the deprecated file to be deleted.")
		assert.FileExists(t, unrelatedFile, "Expected the unrelated file to be untouched.")
	})

	require.NoError(t, os.Remove(unrelatedFile))

	t.Run("prunes_empty_folder", func(t *testing.T) {
		underTest, err := NewReconciler(
			k0sVars,
			&k0sv1beta1.ClusterSpec{
				API: &k0sv1beta1.APISpec{},
				Network: &k0sv1beta1.Network{
					ClusterDomain: "test.local",
					ServiceCIDR:   "99.99.99.0/24",
				},
			},
			testutil.NewFakeClientFactory(),
			&leaderelector.Dummy{Leader: true},
		)
		require.NoError(t, err)

		assert.NoError(t, underTest.Init(context.TODO()))

		assert.NoDirExists(t, folder, "Expected the empty deprecated folder to be deleted.")
		assert.DirExists(t, k0sVars.ManifestsDir, "Expected the manifests folder to be untouched.")
	})
}

func TestReconciler_ReconcileLoopClosesDoneChannels(t *testing.T) {
	underTest := Reconciler{
		log:           logrus.New(),
		cleaner:       newMockCleaner(),
		leaderElector: &leaderelector.Dummy{Leader: true},
	}
	underTest.cleaner.init()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Prepare update channel for three updates
	updates, firstDone, thirdDone := make(chan updateFunc, 3), make(chan error, 1), make(chan error, 1)

	// Put in the first update: It will cancel the context when done and then
	// send the other updates.
	updates <- func(*snapshot) chan<- error { return firstDone }
	go func() {
		defer close(updates) // No more updates after the ones from below.

		<-firstDone
		cancel()

		// Put in the second update with a nil done channel that should be ignored.
		updates <- func(*snapshot) chan<- error { return nil }

		// Put in the third update that should receive the context's error.
		updates <- func(*snapshot) chan<- error { return thirdDone }
	}()

	underTest.runReconcileLoop(ctx, updates, nil)

	select {
	// The first channel should have been consumed by the goroutine that closes
	// the context and the update channel. The reconcile method should return
	// only after the update channel has been closed.
	case _, ok := <-firstDone:
		assert.False(t, ok, "Unexpected element in first done channel")
	default:
		assert.Fail(t, "First done channel not closed")
	}

	assert.Len(t, thirdDone, 1, "Third done channel should contain an element")
	assert.ErrorIs(t, <-thirdDone, ctx.Err(), "Third done channel should contain the context's error")
	select {
	case _, ok := <-thirdDone:
		assert.False(t, ok, "Unexpected element in third done channel")
	default:
		assert.Fail(t, "Third done channel not closed")
	}
}

func TestReconciler_LeaderElection(t *testing.T) {
	var le mockLeaderElector
	cluster := k0sv1beta1.DefaultClusterConfig(nil)
	cleaner := newMockCleaner()
	underTest, err := NewReconciler(
		constant.GetConfig(t.TempDir()),
		&k0sv1beta1.ClusterSpec{
			API: &k0sv1beta1.APISpec{},
			Network: &k0sv1beta1.Network{
				ClusterDomain: "test.local",
				ServiceCIDR:   "99.99.99.0/24",
			},
		},
		testutil.NewFakeClientFactory(),
		&le,
	)
	require.NoError(t, err)
	underTest.cleaner = cleaner

	log := logrus.New()
	log.SetLevel(logrus.DebugLevel)
	underTest.log = log

	require.NoError(t, underTest.Init(context.TODO()))

	mockApplier := installMockApplier(t, underTest)

	require.NoError(t, underTest.Start(context.TODO()))
	t.Cleanup(func() {
		assert.NoError(t, underTest.Stop())
	})

	assert.NoError(t, underTest.Reconcile(context.TODO(), cluster))

	applied := mockApplier.expectApply(t, nil)
	le.activate()
	assert.NotEmpty(t, applied(), "Expected some resources to be applied")
}

func requireKubelet(t *testing.T, resources []*unstructured.Unstructured, name string) string {
	configMap := find(t, "No ConfigMap found with name "+name,
		resources, func(resource *unstructured.Unstructured) bool {
			return resource.GetKind() == "ConfigMap" && resource.GetName() == name
		},
	)
	kubeletConfigYAML, ok, err := unstructured.NestedString(configMap.Object, "data", "kubeletConfiguration")
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

func makeKubeletConfig(t *testing.T, mods ...func(*kubeletConfig)) string {
	kubeletConfig := kubeletConfig{
		TypeMeta: metav1.TypeMeta{
			APIVersion: kubeletv1beta1.SchemeGroupVersion.String(),
			Kind:       "KubeletConfiguration",
		},
		ClusterDNS:         []string{"99.99.99.10"},
		ClusterDomain:      "test.local",
		EventRecordQPS:     pointer.Int32(0),
		FailSwapOn:         pointer.Bool(false),
		RotateCertificates: true,
		ServerTLSBootstrap: true,
		TLSCipherSuites: []string{
			"TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256",
			"TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384",
			"TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305",
			"TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256",
			"TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384",
			"TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305",
			"TLS_RSA_WITH_AES_128_GCM_SHA256",
			"TLS_RSA_WITH_AES_256_GCM_SHA384",
		},
	}

	for _, mod := range mods {
		mod(&kubeletConfig)
	}

	json, err := json.Marshal(&kubeletConfig)
	require.NoError(t, err)
	return string(json)
}

type mockApplier struct {
	ptr atomic.Pointer[[]mockApplierCall]
}

type mockApplierCall = func(resources) error

func (m *mockApplier) expectApply(t *testing.T, retval error) func() resources {
	ch := make(chan resources, 1)

	recv := func() resources {
		select {
		case lastCalled, ok := <-ch:
			if !ok {
				require.Fail(t, "Channel closed unexpectedly")
			}
			return lastCalled

		case <-time.After(10 * time.Second):
			require.Fail(t, "Timed out while waiting for call to apply()")
			return nil // function diverges above
		}
	}

	send := func(r resources) error {
		defer close(ch)
		if r == nil { // called during test cleanup
			return nil
		}

		ch <- r
		return retval
	}

	for {
		calls := m.ptr.Load()
		len := len(*calls)
		newCalls := make([]mockApplierCall, len+1)
		copy(newCalls, *calls)
		newCalls[len] = send
		if m.ptr.CompareAndSwap(calls, &newCalls) {
			break
		}
	}

	return recv
}

func installMockApplier(t *testing.T, underTest *Reconciler) *mockApplier {
	mockApplier := mockApplier{}
	mockApplier.ptr.Store(new([]mockApplierCall))

	underTest.mu.Lock()
	defer underTest.mu.Unlock()

	underTestState := underTest.state
	initialized, ok := underTestState.(reconcilerInitialized)
	require.True(t, ok, "unexpected state: %T", underTestState)
	require.NotNil(t, initialized.apply)
	t.Cleanup(func() {
		for _, call := range *mockApplier.ptr.Swap(nil) {
			assert.NoError(t, call(nil))
		}
	})

	initialized.apply = func(ctx context.Context, r resources) error {
		if r == nil {
			panic("cannot call apply() with nil resources")
		}

		for {
			expected := mockApplier.ptr.Load()
			if len(*expected) < 1 {
				panic("unexpected call to apply")
			}

			newExpected := (*expected)[1:]
			if mockApplier.ptr.CompareAndSwap(expected, &newExpected) {
				return (*expected)[0](r)
			}
		}
	}

	underTest.state = initialized
	return &mockApplier
}

type mockCleaner struct {
	mu    sync.Cond
	state atomic.Value
}

func newMockCleaner() *mockCleaner {
	return &mockCleaner{
		mu: sync.Cond{L: new(sync.Mutex)},
	}
}

func (m *mockCleaner) assertState(t *testing.T, state string) {
	t.Helper()
	assert.Equal(t, state, m.state.Load(), "Unexpected cleaner state")
}

func (m *mockCleaner) awaitState(t *testing.T, state string) {
	t.Helper()

	var timeout bool
	timeouter := time.AfterFunc(10*time.Second, func() {
		m.mu.L.Lock()
		defer m.mu.L.Unlock()
		timeout = true
		m.mu.Broadcast()
	})
	defer timeouter.Stop()

	m.mu.L.Lock()
	defer m.mu.L.Unlock()

	for {
		if timeout {
			require.Fail(t, "Timeout while awaiting cleaner state", state)
		}
		if m.state.Load() == state {
			return
		}
		m.mu.Wait()
	}
}

func (m *mockCleaner) init() {
	if !m.state.CompareAndSwap(nil, "init") {
		panic(fmt.Sprintf("unexpected call to init(): %v", m.state.Load()))
	}
	m.mu.Broadcast()
}

func (m *mockCleaner) reconciled(ctx context.Context) {
	state := m.state.Load()
	if (state != "init" && state != "reconciled") || !m.state.CompareAndSwap(state, "reconciled") {
		panic(fmt.Sprintf("unexpected call to reconciled(): %v", state))
	}

	m.mu.Broadcast()
}

func (m *mockCleaner) stop() {
	state := m.state.Load()
	if state == "stop" || !m.state.CompareAndSwap(state, "stop") {
		panic(fmt.Sprintf("unexpected call to stop(): %v", state))
	}
	m.mu.Broadcast()
}

type mockLeaderElector struct {
	mu       sync.Mutex
	leader   bool
	acquired []func()
}

func (e *mockLeaderElector) activate() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.leader {
		e.leader = true
		for _, fn := range e.acquired {
			fn()
		}
	}
}

func (e *mockLeaderElector) IsLeader() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.leader
}

func (e *mockLeaderElector) AddAcquiredLeaseCallback(fn func()) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.acquired = append(e.acquired, fn)
	if e.leader {
		fn()
	}
}

func (e *mockLeaderElector) AddLostLeaseCallback(func()) {
	panic("not expected to be called in tests")
}
