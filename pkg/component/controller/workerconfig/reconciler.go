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
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"net"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	goruntime "runtime"

	"github.com/cloudflare/cfssl/log"
	"github.com/k0sproject/k0s/pkg/apis/k0s.k0sproject.io/v1beta1"
	"github.com/k0sproject/k0s/pkg/applier"
	"github.com/k0sproject/k0s/pkg/component"
	"github.com/k0sproject/k0s/pkg/constant"
	kubeutil "github.com/k0sproject/k0s/pkg/kubernetes"
	"github.com/k0sproject/k0s/pkg/kubernetes/watch"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	kubeletv1beta1 "k8s.io/kubelet/config/v1beta1"
	"k8s.io/utils/pointer"

	"github.com/sirupsen/logrus"
	"go.uber.org/multierr"
	"golang.org/x/exp/slices"
	"sigs.k8s.io/yaml"
)

type resources = []*unstructured.Unstructured

// type mtex = TalkingMutex

type mtex = sync.Mutex

// Reconciler maintains ConfigMaps that hold configuration to be
// used on k0s worker nodes, depending on their selected worker profile.
type Reconciler struct {
	log logrus.FieldLogger

	konnectivityEnabled bool
	watchAPIServers     bool
	cleaner             cleaner

	mu            mtex
	state         interface{}
	clusterDNSIP  net.IP
	clusterDomain string
	// doApply  func(context.Context, resources) error
	// cancel   context.CancelFunc
	// stopChan <-chan struct{}

	// snapshot    snapshot
	// lastApplied *snapshot
}

var _ component.Component = (*Reconciler)(nil)
var _ component.Reconciler = (*Reconciler)(nil)

type updateFunc = func(*snapshot) chan<- error

type reconcilerStarted struct {
	stop    func()
	stopped <-chan struct{}

	updates chan<- updateFunc
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

type reconcilerCreated struct {
	clientFactory kubeutil.ClientFactoryInterface
}

// NewReconciler creates a new reconciler for worker configurations.
func NewReconciler(k0sVars constant.CfgVars, nodeSpec *v1beta1.ClusterSpec, clientFactory kubeutil.ClientFactoryInterface, konnectivityEnabled bool) (*Reconciler, error) {
	log := logrus.WithFields(logrus.Fields{"component": "workerconfig.Reconciler"})

	clusterDNSIPString, err := nodeSpec.Network.DNSAddress()
	if err != nil {
		return nil, err
	}
	clusterDNSIP := net.ParseIP(clusterDNSIPString)
	if clusterDNSIP == nil {
		return nil, fmt.Errorf("not an IP address: %q", clusterDNSIPString)
	}

	reconciler := &Reconciler{
		log: log,

		clusterDomain:       nodeSpec.Network.ClusterDomain,
		clusterDNSIP:        clusterDNSIP,
		konnectivityEnabled: konnectivityEnabled,
		watchAPIServers:     !nodeSpec.API.TunneledNetworkingMode,
		cleaner: &kubeletConfigCleaner{
			log: log,

			dir:           filepath.Join(k0sVars.ManifestsDir, "kubelet"),
			clientFactory: clientFactory,
		},
		state: reconcilerCreated{clientFactory},
	}

	return reconciler, nil
}

type reconcilerInitialized struct {
	client kubernetes.Interface
	apply  func(context.Context, resources) error
}

func (r *Reconciler) Init(context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	var created reconcilerCreated
	switch state := r.state.(type) {
	case reconcilerCreated:
		created = state
	default:
		return fmt.Errorf("cannot initialize: %T", state)
	}

	client, err := created.clientFactory.GetClient()
	if err != nil {
		return err
	}

	apply := func(ctx context.Context, resources resources) error {
		dynamicClient, err := created.clientFactory.GetDynamicClient()
		if err != nil {
			return err
		}
		discoveryClient, err := created.clientFactory.GetDiscoveryClient()
		if err != nil {
			return err
		}

		return (&applier.Stack{
			Name:      "k0s-" + constant.WorkerConfigComponentName,
			Client:    dynamicClient,
			Discovery: discoveryClient,
			Resources: resources,
		}).Apply(ctx, true)
	}

	r.cleaner.init()
	r.state = reconcilerInitialized{client, apply}
	return nil
}

func (r *Reconciler) Start(context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	var initialized reconcilerInitialized
	switch state := r.state.(type) {
	case reconcilerInitialized:
		initialized = state
	default:
		return fmt.Errorf("cannot start: %T", state)
	}

	// FIXME leader election

	var started reconcilerStarted
	updates := make(chan updateFunc, 1)
	started.updates = updates

	reconcilersCtx, cancelReconcilers := context.WithCancel(context.Background())
	var reconcilersDone sync.WaitGroup

	updatersCtx, cancelUpdaters := context.WithCancel(context.Background())
	var updatersDone sync.WaitGroup

	{ // set up stop facility
		stopped := make(chan struct{})
		var stopCalled atomic.Bool
		started.stop = func() {
			if stopCalled.Swap(true) {
				return
			}
			defer close(stopped)

			r.log.Debug("Stopping: Stop cleaner")
			r.cleaner.stop()

			r.log.Debug("Stopping: Canceling updaters")
			cancelUpdaters()
			r.log.Debug("Stopping: Waiting for updaters to exit")
			updatersDone.Wait()

			r.log.Debug("Stopping: Canceling reconcilers")
			close(updates)
			cancelReconcilers()
			r.log.Debug("Stopping: Waiting for reconcilers to exit")
			reconcilersDone.Wait()
			r.log.Debug("Stopping: Done")
		}
		started.stopped = stopped
	}

	reconcilersDone.Add(1)
	go func() {
		defer reconcilersDone.Done()
		defer r.log.Info("Reconciliation loop done")
		r.log.Info("Starting reconciliation loop")
		r.runReconcileLoop(reconcilersCtx, updates, initialized.apply)
	}()

	if r.watchAPIServers {
		updatersDone.Add(1)
		go func() {
			defer updatersDone.Done()
			buf := make([]byte, 64)
			n := goruntime.Stack(buf, false)
			gr := string(bytes.Fields(buf[:n])[1])
			defer r.log.Info("API Server watch done -- ", gr)
			r.log.Debug("Starting API server watch -- ", gr)
			var lastObservedVersion string
			wait.UntilWithContext(updatersCtx, func(ctx context.Context) {
				var err error
				lastObservedVersion, err = watchAPIServers(ctx, lastObservedVersion, initialized.client, updates)
				if err != nil {
					r.log.WithError(err).Errorf(
						"Failed to watch for API server addresses (last observed resource version is %q)",
						lastObservedVersion,
					)
				}
			}, 10*time.Second)
		}()
	}

	r.state = started
	return nil
}

func (r *Reconciler) runReconcileLoop(ctx context.Context, updates <-chan updateFunc, apply func(context.Context, resources) error) {
	var desiredState, reconciledState snapshot

	runReconciliation := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}

		if desiredState.configSnapshot == nil || (r.watchAPIServers && len(desiredState.apiServers) < 1) {
			r.log.Debug("Skipping reconciliation, snapshot not yet complete")
			return nil
		}

		if reflect.DeepEqual(&reconciledState, &desiredState) {
			r.log.Debug("Skipping reconciliation, nothing changed")
			return nil
		}

		stateToReconcile := desiredState.DeepCopy()
		resources, err := generateResources(r.clusterDomain, r.clusterDNSIP, stateToReconcile)
		if err != nil {
			return fmt.Errorf("failed to generate resources for worker configuration: %w", err)
		}

		r.log.Debug("Updating worker configuration ...")

		err = apply(ctx, resources)
		if err != nil {
			return fmt.Errorf("failed to apply resources for worker configuration: %w", err)
		}

		stateToReconcile.DeepCopyInto(&reconciledState)

		r.log.Info("Worker configuration updated")
		r.cleaner.reconciled(ctx)
		return nil
	}

	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	var lastRecoFailed bool

	for {
		select {
		case updateFunc, ok := <-updates:
			if !ok {
				return
			}
			done := updateFunc(&desiredState)
			func() {
				if done != nil {
					defer close(done)
				}
				err := runReconciliation()
				lastRecoFailed = err != nil
				if done != nil {
					done <- err
				}
			}()

		case <-ticker.C: // Retry failed reconciliations every minute
			if lastRecoFailed {
				if err := runReconciliation(); err != nil {
					r.log.WithError(err).Error("Failed to recover from previously failed reconciliation")
					continue
				}

				lastRecoFailed = false
			}
		}
	}
}

func (r *Reconciler) Reconcile(ctx context.Context, cluster *v1beta1.ClusterConfig) error {
	done, err := func() (<-chan error, error) {
		r.mu.Lock()
		defer r.mu.Unlock()

		started, ok := r.state.(reconcilerStarted)
		if !ok {
			return nil, errors.New("not started, cannot reconcile")
		}

		configSnapshot, err := makeConfigSnapshot(r.log, cluster.Spec, r.konnectivityEnabled)
		if err != nil {
			return nil, fmt.Errorf("failed to snapshot the cluster configuration: %w", err)
		}

		done := make(chan error, 1)
		update := func(s *snapshot) chan<- error {
			s.configSnapshot = configSnapshot
			return done
		}

		select {
		case started.updates <- update:
			return done, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}()
	if err != nil {
		return err
	}

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

type reconcilerStopped struct {
	stopped <-chan struct{}
}

func (r *Reconciler) Stop() error {
	r.log.Debug("Stop called")
	stopped, err := func() (<-chan struct{}, error) {
		r.log.Debug("Stop: acquiring lock")
		r.mu.Lock()
		defer r.mu.Unlock()

		switch state := r.state.(type) {
		case nil:
			r.state = reconcilerStopped{}
			r.cleaner.stop()
			return nil, nil
		case reconcilerStarted:
			r.state = reconcilerStopped{state.stopped}
			go state.stop()
			return state.stopped, nil
		case reconcilerStopped:
			return state.stopped, nil
		default:
			return nil, fmt.Errorf("don't know how to stop: %T", state)
		}
	}()
	if err != nil {
		return err
	}

	if stopped == nil {
		r.log.Debug("Stop: already stopped")
		return nil
	}

	r.log.Debug("Stop: awaiting")
	<-stopped
	r.mu.Lock()
	defer r.mu.Unlock()
	r.state = reconcilerStopped{}
	r.log.Debug("Stop: received")
	return nil
}

func watchAPIServers(ctx context.Context, lastObservedVersion string, client kubernetes.Interface, updates chan<- updateFunc) (string, error) {
	err := watch.Endpoints(client.CoreV1().Endpoints("default")).WithObjectName("kubernetes").Until(
		ctx, func(endpoints *corev1.Endpoints) (bool, error) {
			err := func() error {
				apiServers, err := extractAPIServerAddresses(endpoints)
				if err != nil {
					return err
				}

				return updateAPIServers(ctx, apiServers, updates)
			}()
			if err != nil {
				return false, err
			}

			lastObservedVersion = endpoints.ResourceVersion
			return false, nil
		},
	)

	return lastObservedVersion, err
}

func extractAPIServerAddresses(endpoints *corev1.Endpoints) (apiServers, error) {
	var warnings error
	apiServers := []hostPort{}

	for sIdx, subset := range endpoints.Subsets {
		var ports []uint16
		for pIdx, port := range subset.Ports {
			// FIXME: is a more sophisticated port detection required?
			// E.g. does the service object need to be inspected?
			if port.Protocol != corev1.ProtocolTCP || port.Name != "https" {
				continue
			}

			if port.Port < 0 || port.Port > math.MaxUint16 {
				path := field.NewPath("subsets").Index(sIdx).Child("ports").Index(pIdx).Child("port")
				warning := field.Invalid(path, port.Port, "out of range")
				warnings = multierr.Append(warnings, warning)
				continue
			}

			ports = append(ports, uint16(port.Port))
		}

		if len(ports) < 1 {
			path := field.NewPath("subsets").Index(sIdx)
			warning := field.Forbidden(path, "no suitable TCP/https ports found")
			warnings = multierr.Append(warnings, warning)
			continue
		}

		for aIdx, address := range subset.Addresses {
			host := address.IP
			if host == "" {
				host = address.Hostname
			}
			if host == "" {
				path := field.NewPath("addresses").Index(aIdx)
				warning := field.Forbidden(path, "neither ip nor hostname specified")
				warnings = multierr.Append(warnings, warning)
				continue
			}

			for _, port := range ports {
				apiServers = append(apiServers, hostPort{host, port})
			}
		}
	}

	if len(apiServers) < 1 {
		// Never update the API servers with an empty list. This cannot
		// be right in any case, and would never recover.
		return nil, multierr.Append(errors.New("no API server addresses discovered"), warnings)
	}

	return apiServers, nil
}

func updateAPIServers(ctx context.Context, apiServers apiServers, updates chan<- updateFunc) error {
	done := make(chan error, 1)
	update := func(s *snapshot) chan<- error {
		s.apiServers = apiServers
		return done
	}

	log.Debug("Updating API servers: Enqueueing update")

	select {
	case updates <- update:
		select {
		case err := <-done:
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	case <-ctx.Done():
		return ctx.Err()
	}
}

type resource interface {
	runtime.Object
	metav1.Object
}

func generateResources(clusterDomain string, clusterDNSIP net.IP, snapshot *snapshot) (resources, error) {
	builder := &configBuilder{
		apiServers:    snapshot.apiServers,
		specSnapshot:  snapshot.specSnapshot,
		clusterDNSIP:  clusterDNSIP,
		clusterDomain: clusterDomain,
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
	clusterDNSIP  net.IP
	clusterDomain string
}

func (b *configBuilder) build() *workerConfig {
	c := &workerConfig{
		apiServers: append((apiServers)(nil), b.apiServers...),
		kubeletConfiguration: kubeletv1beta1.KubeletConfiguration{
			TypeMeta: metav1.TypeMeta{
				APIVersion: kubeletv1beta1.SchemeGroupVersion.String(),
				Kind:       "KubeletConfiguration",
			},
			ClusterDNS:    []string{b.clusterDNSIP.String()},
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
		konnectivityAgentPort:  b.konnectivityAgentPort,
		defaultImagePullPolicy: b.defaultImagePullPolicy,
	}

	return c
}
