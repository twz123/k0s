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
	"math"
	"path/filepath"
	"reflect"
	goruntime "runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/k0sproject/k0s/pkg/apis/k0s.k0sproject.io/v1beta1"
	"github.com/k0sproject/k0s/pkg/applier"
	"github.com/k0sproject/k0s/pkg/component"
	"github.com/k0sproject/k0s/pkg/constant"
	kubeutil "github.com/k0sproject/k0s/pkg/kubernetes"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	watchtools "k8s.io/client-go/tools/watch"
	kubeletv1beta1 "k8s.io/kubelet/config/v1beta1"
	"k8s.io/utils/pointer"

	"github.com/sirupsen/logrus"
	"golang.org/x/exp/slices"
	"sigs.k8s.io/yaml"
)

type resources = []*unstructured.Unstructured

// type mtex = talkingMutex
type mtex = sync.Mutex

// Reconciler maintains ConfigMaps that hold configuration to be
// used on k0s worker nodes, depending on their selected worker profile.
type Reconciler struct {
	log logrus.FieldLogger

	clientFactory kubeutil.ClientFactoryInterface
	cleaner       cleaner

	mu    mtex
	state atomic.Pointer[any]
	// doApply  func(context.Context, resources) error
	// cancel   context.CancelFunc
	// stopChan <-chan struct{}

	// snapshot    snapshot
	// lastApplied *snapshot
}

var _ component.Component = (*Reconciler)(nil)
var _ component.Reconciler = (*Reconciler)(nil)

type reconcilerStarted struct {
	stop        func()
	mu          mtex
	doApply     func(context.Context, resources) error
	snapshot    snapshot
	lastApplied *snapshot
}

type TalkingMutex struct {
	mu sync.Mutex
}

func (m *TalkingMutex) Lock() {
	info := info()
	logrus.Infof("Locking %p: %s", &m.mu, info)
	m.mu.Lock()
	defer func() {
		if r := recover(); r != nil {
			m.mu.Unlock()
			panic(r)
		}
	}()
	logrus.Infof("Locked %p: %s", &m.mu, info)
}

func (m *TalkingMutex) Unlock() {
	info := info()
	logrus.Infof("Unlocking %p: %s", &m.mu, info)
	m.mu.Unlock()
	logrus.Infof("Unlocked %p: %s", &m.mu, info)
}

func info() string {
	pc, file, no, ok := goruntime.Caller(2)
	if !ok {
		return "(unknown)"
	}
	if details := goruntime.FuncForPC(pc); details != nil {
		return fmt.Sprintf("%s (%s:%d)", details.Name(), filepath.Base(file), no)
	}

	return fmt.Sprintf("%s:%d", filepath.Base(file), no)
}

type reconcilerStopped struct{}

// NewReconciler creates a new reconciler for worker configurations.
func NewReconciler(k0sVars constant.CfgVars, clientFactory kubeutil.ClientFactoryInterface) *Reconciler {
	log := logrus.WithFields(logrus.Fields{"component": "workerconfig.Reconciler"})

	reconciler := &Reconciler{
		log: log,

		clientFactory: clientFactory,
		cleaner: &kubeletConfigCleaner{
			log: log,

			dir:           filepath.Join(k0sVars.ManifestsDir, "kubelet"),
			clientFactory: clientFactory,
		},
	}

	return reconciler
}

func (r *Reconciler) Init(context.Context) error {
	r.cleaner.init()
	return nil
}

func (*Reconciler) Healthy() error { return nil }

func (r *Reconciler) Start(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.state.Load() != nil {
		return errors.New("cannot run")
	}

	// FIXME leader election

	client, err := r.clientFactory.GetClient()
	if err != nil {
		return err
	}
	dynamicClient, err := r.clientFactory.GetDynamicClient()
	if err != nil {
		return err
	}
	discoveryClient, err := r.clientFactory.GetDiscoveryClient()
	if err != nil {
		return err
	}

	stack := &applier.Stack{
		Name:      "k0s-" + constant.WorkerConfigComponentName,
		Client:    dynamicClient,
		Discovery: discoveryClient,
	}

	watchStopped := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())

	doApply := func(ctx context.Context, resources resources) error {
		stack.Resources = resources
		defer func() { stack.Resources = nil }()
		return stack.Apply(ctx, true)
	}

	started := &reconcilerStarted{
		doApply: doApply,
	}
	started.stop = func() {
		cancel()
		<-watchStopped
		started.mu.Lock()
		defer started.mu.Unlock()
		started.doApply = nil
	}

	go func() {
		defer close(watchStopped)
		wait.UntilWithContext(ctx, func(ctx context.Context) {
			err := r.watchAPIServers(ctx, started, client)
			if err != nil {
				r.log.WithError(err).Error("Failed to watch for API server addresses")
			}
		}, 10*time.Second)
	}()

	r.store(started)
	return nil
}

func (r *Reconciler) store(state any) {
	r.state.Store(&state)
}

func (r *Reconciler) load() any {
	ptr := r.state.Load()
	if ptr == nil {
		return nil
	}
	return *ptr
}

func (r *Reconciler) Reconcile(ctx context.Context, cluster *v1beta1.ClusterConfig) error {
	state, ok := r.load().(*reconcilerStarted)
	if !ok {
		return errors.New("not started, cannot reconcile")
	}

	state.mu.Lock()

	defer state.mu.Unlock()
	if state.doApply == nil {
		return errors.New("stopped concurrently")
	}

	return r.updateConfigSnapshot(ctx, state, cluster.Spec)
}

func (r *Reconciler) Stop() error {
	stop, err := func() (func(), error) {
		r.mu.Lock()
		defer r.mu.Unlock()

		switch state := r.load().(type) {
		case *reconcilerStarted:
			return state.stop, nil
		case nil, reconcilerStopped:
			return func() {}, nil
		default:
			return nil, fmt.Errorf("don't know how to stop: %T", state)
		}
	}()

	if err != nil {
		return err
	}

	stop()
	r.store(reconcilerStopped{})
	r.cleaner.stop()
	return nil
}

func (r *Reconciler) watchAPIServers(ctx context.Context, state *reconcilerStarted, client kubernetes.Interface) error {
	endpoints := client.CoreV1().Endpoints("default")
	fieldSelector := fields.OneTermEqualSelector(metav1.ObjectNameField, "kubernetes").String()

	var initialResourceVersion string
	{
		initial, err := endpoints.List(ctx, metav1.ListOptions{FieldSelector: fieldSelector})

		if err != nil {
			return err
		}

		initialResourceVersion = initial.ResourceVersion

		if len(initial.Items) != 1 {
			logrus.Debug("Didn't find exactly one Endpoints object for API servers, but", len(initial.Items))
		}
		if len(initial.Items) > 0 {
			if err := r.updateAPIServers(ctx, state, &initial.Items[0]); err != nil {
				return err
			}
		}
	}

	changes, err := watchtools.NewRetryWatcher(initialResourceVersion, &cache.ListWatch{
		WatchFunc: func(opts metav1.ListOptions) (watch.Interface, error) {
			opts.FieldSelector = fieldSelector
			return endpoints.Watch(ctx, opts)
		},
	})
	if err != nil {
		return err
	}
	defer changes.Stop()

	for {
		select {
		case event, ok := <-changes.ResultChan():
			if !ok {
				return errors.New("result channel closed unexpectedly")
			}

			switch event.Type {
			case watch.Added, watch.Modified:
				logrus.Infof("Changes to API servers: %s %#+v", event.Type, event.Object)
				e, ok := event.Object.(*corev1.Endpoints)
				if !ok {
					logrus.Warnf("Unsupported type %T: %#+v", event.Type, event.Object)
					continue
				}
				if err := func() error {
					state.mu.Lock()
					defer state.mu.Unlock()
					return r.updateAPIServers(ctx, state, e)
				}(); err != nil {
					return err
				}
			case watch.Deleted:
				logrus.Warnf("Ignoring deletion of %#+v", event.Object)
			case watch.Error:
				logrus.WithError(apierrors.FromObject(event.Object)).Error("Error while watching API server endpoints")
			default:
				logrus.Debugf("Ignoring event for API servers: %s %#+v", event.Type, event.Object)
			}

		case <-ctx.Done():
			return nil
		}
	}
}

func (r *Reconciler) updateConfigSnapshot(ctx context.Context, state *reconcilerStarted, spec *v1beta1.ClusterSpec) error {
	configSnapshot, err := makeConfigSnapshot(r.log, spec)
	if err != nil {
		return fmt.Errorf("failed to snapshot the cluster configuration: %w", err)
	}

	state.snapshot.configSnapshot = configSnapshot
	return r.snapshotUpdated(ctx, state)
}

func (r *Reconciler) updateAPIServers(ctx context.Context, state *reconcilerStarted, e *corev1.Endpoints) error {
	apiServers := []hostPort{}
	for sIdx, subset := range e.Subsets {
		var ports []uint16
		for _, port := range subset.Ports {
			// FIXME: is a more sophisticated port detection required?
			// E.g. does the service object need to be inspected?
			if port.Protocol == corev1.ProtocolTCP && port.Name == "https" {
				if port.Port > 0 && port.Port <= math.MaxUint16 {
					ports = append(ports, uint16(port.Port))
				}
			}
		}

		if len(ports) < 1 {
			r.log.Warnf("No suitable ports found in subset %d: %+#v", sIdx, subset.Ports)
			continue
		}

		for aIdx, address := range subset.Addresses {
			host := address.IP
			if host == "" {
				host = address.Hostname
			}
			if host == "" {
				r.log.Warnf("Failed to get host from address %d/%d: %+#v", sIdx, aIdx, address)
				continue
			}

			for _, port := range ports {
				apiServers = append(apiServers, hostPort{host, port})
			}
		}
	}

	state.mu.Lock()
	defer state.mu.Unlock()

	state.snapshot.apiServers = apiServers
	return r.snapshotUpdated(ctx, state)
}

func (r *Reconciler) snapshotUpdated(ctx context.Context, state *reconcilerStarted) error {
	if !state.snapshot.isComplete() {
		r.log.Debug("Skipping reconciliation, snapshot not yet complete")
		return nil
	}

	if state.lastApplied != nil && reflect.DeepEqual(state.lastApplied, &state.snapshot) {
		r.log.Debug("Skipping reconciliation, nothing changed")
		return nil
	}

	snapshotToApply := state.snapshot.DeepCopy()
	resources, err := generateResources(snapshotToApply)
	if err != nil {
		return fmt.Errorf("failed to generate resources for worker configuration: %w", err)
	}

	r.log.Debug("Updating worker configuration ...")

	err = state.doApply(ctx, resources)
	if err != nil {
		state.lastApplied = nil
		return fmt.Errorf("failed to apply resources for worker configuration: %w", err)
	}
	state.lastApplied = snapshotToApply

	r.log.Info("Worker configuration updated")

	r.cleaner.reconciled(ctx)

	return nil
}

type resource interface {
	runtime.Object
	metav1.Object
}

func generateResources(snapshot *snapshot) ([]*unstructured.Unstructured, error) {
	builder := &configBuilder{
		apiServers:   snapshot.apiServers,
		specSnapshot: snapshot.specSnapshot,
	}

	configMaps, err := buildConfigMaps(builder, snapshot.profiles)
	if err != nil {
		return nil, err
	}

	// Golang won't allow covariant things like
	//   objects := append(buildRBACResources(configMaps), configMaps...)
	// Hence the for loop... ¯\_(ツ)_/¯
	objects := buildRBACResources(configMaps)
	for _, configMap := range configMaps {
		objects = append(objects, configMap)
	}

	// Ensure a stable order, so that reflect.DeepEqual on slices will work.
	slices.SortFunc(objects, func(l, r resource) bool {
		x := strings.Join([]string{l.GetObjectKind().GroupVersionKind().Kind, l.GetNamespace(), l.GetName()}, "/")
		y := strings.Join([]string{r.GetObjectKind().GroupVersionKind().Kind, r.GetNamespace(), r.GetName()}, "/")
		return x < y
	})

	resources, err := applier.ToUnstructuredSlice(nil, objects...)
	if err != nil {
		return nil, err
	}

	return resources, nil
}

func buildConfigMaps(builder *configBuilder, profiles v1beta1.WorkerProfiles) ([]*corev1.ConfigMap, error) {
	configs := make(map[string]*workerConfig)

	config := builder.build()
	config.kubeletConfiguration.CgroupsPerQOS = pointer.Bool(true)
	configs["default"] = config

	config = builder.build()
	config.kubeletConfiguration.CgroupsPerQOS = pointer.Bool(false)
	configs["default-windows"] = config

	for _, profile := range profiles {
		config, ok := configs[profile.Name]
		if !ok {
			config = builder.build()
		}
		if err := yaml.Unmarshal(profile.Config, &config.kubeletConfiguration); err != nil {
			return nil, fmt.Errorf("failed to decode worker profile %q: %w", profile.Name, err)
		}
		configs[profile.Name] = config
	}

	var configMaps []*corev1.ConfigMap
	for name, config := range configs {
		configMap, err := config.toConfigMap(name)
		if err != nil {
			return nil, fmt.Errorf("failed to generate ConfigMap for worker profile %q: %w", name, err)
		}
		configMaps = append(configMaps, configMap)
	}

	return configMaps, nil
}

func buildRBACResources(configMaps []*corev1.ConfigMap) []resource {
	configMapNames := make([]string, len(configMaps))
	for i, configMap := range configMaps {
		configMapNames[i] = configMap.ObjectMeta.Name
	}

	// Not strictly necessary, but it guarantees a stable ordering.
	sort.Strings(configMapNames)

	meta := metav1.ObjectMeta{
		Name:      fmt.Sprintf("system:bootstrappers:%s", constant.WorkerConfigComponentName),
		Namespace: "kube-system",
		Labels:    applier.CommonLabels(constant.WorkerConfigComponentName),
	}

	var objects []resource
	objects = append(objects, &rbacv1.Role{
		ObjectMeta: meta,
		Rules: []rbacv1.PolicyRule{{
			APIGroups:     []string{""},
			Resources:     []string{"configmaps"},
			Verbs:         []string{"get"},
			ResourceNames: configMapNames,
		}},
	})

	objects = append(objects, &rbacv1.RoleBinding{
		ObjectMeta: meta,
		RoleRef: rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "Role",
			Name:     meta.Name,
		},
		Subjects: []rbacv1.Subject{{
			APIGroup: rbacv1.GroupName,
			Kind:     rbacv1.GroupKind,
			Name:     "system:bootstrappers",
		}, {
			APIGroup: rbacv1.GroupName,
			Kind:     rbacv1.GroupKind,
			Name:     "system:nodes",
		}},
	})

	meta = metav1.ObjectMeta{
		Name:      "system:bootstrappers:discovery",
		Namespace: "default",
		Labels:    applier.CommonLabels(constant.WorkerConfigComponentName),
	}

	objects = append(objects, &rbacv1.Role{
		ObjectMeta: meta,
		Rules: []rbacv1.PolicyRule{{
			APIGroups:     []string{""},
			Resources:     []string{"endpoints"},
			Verbs:         []string{"get", "list", "watch"},
			ResourceNames: []string{"kubernetes"},
		}},
	})

	objects = append(objects, &rbacv1.RoleBinding{
		ObjectMeta: meta,
		RoleRef: rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "Role",
			Name:     meta.Name,
		},
		Subjects: []rbacv1.Subject{{
			APIGroup: rbacv1.GroupName,
			Kind:     rbacv1.GroupKind,
			Name:     "system:bootstrappers",
		}, {
			APIGroup: rbacv1.GroupName,
			Kind:     rbacv1.GroupKind,
			Name:     "system:nodes",
		}},
	})

	return objects
}

type configBuilder struct {
	apiServers
	specSnapshot
}

func (b *configBuilder) build() *workerConfig {
	c := &workerConfig{
		apiServers: append((apiServers)(nil), b.apiServers...),
		kubeletConfiguration: kubeletv1beta1.KubeletConfiguration{
			TypeMeta: metav1.TypeMeta{
				APIVersion: kubeletv1beta1.SchemeGroupVersion.String(),
				Kind:       "KubeletConfiguration",
			},
			ClusterDNS:    []string{b.dnsAddress},
			ClusterDomain: b.clusterDomain,
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
			FailSwapOn:         pointer.Bool(false),
			RotateCertificates: true,
			ServerTLSBootstrap: true,
			EventRecordQPS:     pointer.Int32(0),
		},
		nodeLocalLoadBalancer:  b.nodeLocalLoadBalancer.DeepCopy(),
		defaultImagePullPolicy: b.defaultImagePullPolicy,
	}

	return c
}
