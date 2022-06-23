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
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"

	"github.com/k0sproject/k0s/pkg/apis/k0s.k0sproject.io/v1beta1"
	"github.com/k0sproject/k0s/pkg/applier"
	"github.com/k0sproject/k0s/pkg/component"
	"github.com/k0sproject/k0s/pkg/constant"
	"github.com/k0sproject/k0s/pkg/kubernetes"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	kubeletv1beta1 "k8s.io/kubelet/config/v1beta1"
	"k8s.io/utils/pointer"

	"github.com/sirupsen/logrus"
	"golang.org/x/exp/slices"
	"sigs.k8s.io/yaml"
)

type resources = []*unstructured.Unstructured

// Reconciler maintains ConfigMaps that hold configuration to be
// used on k0s worker nodes, depending on their selected worker profile.
type Reconciler struct {
	log logrus.FieldLogger

	clientFactory kubernetes.ClientFactoryInterface
	cleaner       cleaner

	mu          sync.Mutex
	doApply     func(context.Context, resources) error
	lastApplied resources
}

var _ component.Component = (*Reconciler)(nil)
var _ component.Reconciler = (*Reconciler)(nil)

// NewReconciler creates a new reconciler for worker configurations.
func NewReconciler(k0sVars constant.CfgVars, clientFactory kubernetes.ClientFactoryInterface) *Reconciler {
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

	// FIXME leader election

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

	r.doApply = func(ctx context.Context, resources resources) error {
		stack.Resources = resources
		defer func() { stack.Resources = nil }()
		return stack.Apply(ctx, true)
	}

	return nil
}

func (r *Reconciler) Reconcile(ctx context.Context, cluster *v1beta1.ClusterConfig) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.doApply == nil {
		return errors.New("not running, cannot reconcile")
	}

	resources, err := generateResources(cluster)
	if err != nil {
		return fmt.Errorf("failed to generate resources for worker configuration: %w", err)
	}

	if r.lastApplied != nil && reflect.DeepEqual(r.lastApplied, resources) {
		r.log.Debug("Skipping reconciliation, nothing changed")
		return nil
	}

	r.log.Debug("Updating worker configuration ...")

	err = r.doApply(ctx, resources)
	if err != nil {
		r.lastApplied = nil
		return fmt.Errorf("failed to apply resources for worker configuration: %w", err)
	}
	r.lastApplied = resources

	r.log.Info("Worker configuration updated")

	r.cleaner.reconciled(ctx)

	return nil
}

func (r *Reconciler) Stop() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.doApply = nil
	r.cleaner.stop()
	return nil
}

type resource interface {
	runtime.Object
	metav1.Object
}

func generateResources(cluster *v1beta1.ClusterConfig) ([]*unstructured.Unstructured, error) {
	dnsAddress, err := cluster.Spec.Network.DNSAddress()
	if err != nil {
		return nil, fmt.Errorf("failed to get DNS address from ClusterConfig: %w", err)
	}
	profiles := append((v1beta1.WorkerProfiles)(nil), cluster.Spec.WorkerProfiles...)

	builder := &configBuilder{
		apiServers:    []apiServer{{"127.0.0.1", 6443}},
		dnsAddress:    dnsAddress,
		clusterDomain: cluster.Spec.Network.ClusterDomain,
	}

	configMaps, err := buildConfigMaps(builder, profiles)
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
	config.kubelet.CgroupsPerQOS = pointer.Bool(true)
	configs["default"] = config

	config = builder.build()
	config.kubelet.CgroupsPerQOS = pointer.Bool(false)
	configs["default-windows"] = config

	for _, profile := range profiles {
		config, ok := configs[profile.Name]
		if !ok {
			config = builder.build()
		}
		if err := yaml.Unmarshal(profile.Config, &config.kubelet); err != nil {
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
		Name:      "system:bootstrappers:kubelet-configmaps",
		Namespace: "kube-system",
		Labels:    applier.CommonLabels(constant.WorkerConfigComponentName),
	}

	role := rbacv1.Role{
		ObjectMeta: meta,
		Rules: []rbacv1.PolicyRule{{
			APIGroups:     []string{""},
			Resources:     []string{"configmaps"},
			Verbs:         []string{"get"},
			ResourceNames: configMapNames,
		}},
	}

	binding := rbacv1.RoleBinding{
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
	}

	return []resource{&role, &binding}
}

type configBuilder struct {
	apiServers    apiServers
	dnsAddress    string
	clusterDomain string
}

func (b *configBuilder) build() *workerConfig {
	c := &workerConfig{
		apiServers: append((apiServers)(nil), b.apiServers...),
		kubelet: kubeletv1beta1.KubeletConfiguration{
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
	}

	return c
}
