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
package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"time"

	"github.com/imdario/mergo"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/strategicpatch"
	kubeletconfig "k8s.io/kubelet/config/v1beta1"
	"sigs.k8s.io/yaml"

	//kubeletconfig "k8s.io/kubernetes/pkg/kubelet/apis/config"
	// "k8s.io/kubernetes/pkg/kubelet/apis/config/scheme"

	"github.com/k0sproject/k0s/internal/pkg/dir"
	"github.com/k0sproject/k0s/internal/pkg/templatewriter"
	"github.com/k0sproject/k0s/pkg/apis/k0s.k0sproject.io/v1beta1"
	"github.com/k0sproject/k0s/pkg/component"
	"github.com/k0sproject/k0s/pkg/constant"
	k8sutil "github.com/k0sproject/k0s/pkg/kubernetes"
)

// Dummy checks so we catch easily if we miss some interface implementation
var _ component.ReconcilerComponent = &KubeletConfig{}
var _ component.Component = &KubeletConfig{}

// KubeletConfig is the reconciler for generic kubelet configs
type KubeletConfig struct {
	kubeClientFactory k8sutil.ClientFactoryInterface

	log              *logrus.Entry
	k0sVars          constant.CfgVars
	previousProfiles v1beta1.WorkerProfiles
}

type workerProfile = kubeletconfig.KubeletConfiguration

// NewKubeletConfig creates new KubeletConfig reconciler
func NewKubeletConfig(k0sVars constant.CfgVars, clientFactory k8sutil.ClientFactoryInterface) (*KubeletConfig, error) {
	log := logrus.WithFields(logrus.Fields{"component": "kubeletconfig"})
	return &KubeletConfig{
		kubeClientFactory: clientFactory,
		log:               log,
		k0sVars:           k0sVars,
	}, nil
}

// Init does nothing
func (k *KubeletConfig) Init(_ context.Context) error {
	return nil
}

// Stop does nothign, nothing actually running
func (k *KubeletConfig) Stop() error {
	return nil
}

// Run dumps the needed manifest objects
func (k *KubeletConfig) Run(_ context.Context) error {

	return nil
}

// Reconcile detects changes in configuration and applies them to the component
func (k *KubeletConfig) Reconcile(ctx context.Context, clusterSpec *v1beta1.ClusterConfig) error {
	k.log.Debug("reconcile method called for: KubeletConfig")
	// Check if we actually need to reconcile anything
	defaultProfilesExist, err := k.defaultProfilesExist(ctx)
	if err != nil {
		return err
	}
	if defaultProfilesExist && reflect.DeepEqual(k.previousProfiles, clusterSpec.Spec.WorkerProfiles) {
		k.log.Debugf("default profiles exist and no change in user specified profiles, nothing to reconcile")
		return nil
	}

	manifest, err := k.createProfiles(clusterSpec)
	if err != nil {
		return fmt.Errorf("failed to build final manifest: %v", err)
	}

	if err := k.save(manifest.Bytes()); err != nil {
		return fmt.Errorf("can't write manifest with config maps: %v", err)
	}
	k.previousProfiles = clusterSpec.Spec.WorkerProfiles

	return nil
}

func (k *KubeletConfig) defaultProfilesExist(ctx context.Context) (bool, error) {
	c, err := k.kubeClientFactory.GetClient()
	if err != nil {
		return false, err
	}
	defaultProfileName := formatProfileName("default")
	_, err = c.CoreV1().ConfigMaps("kube-system").Get(ctx, defaultProfileName, metav1.GetOptions{})
	if err != nil && errors.IsNotFound(err) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	return true, nil
}

// Some helpers for workerProfile config values
var (
	// TODO(go1.8): the following should be a single generic helper
	stringPtr = func(s string) *string { return &s }
	boolPtr   = func(b bool) *bool { return &b }
	i32Ptr    = func(i int32) *int32 { return &i }

	zeroSecs = metav1.Duration{0 * time.Second}
)

func (k *KubeletConfig) createProfiles(clusterSpec *v1beta1.ClusterConfig) (*bytes.Buffer, error) {
	dnsAddress, err := clusterSpec.Spec.Network.DNSAddress()
	if err != nil {
		return nil, fmt.Errorf("failed to get DNS address for kubelet config: %v", err)
	}

	manifest := bytes.NewBuffer([]byte{})

	defaultProfile := newWorkerProfile(dnsAddress, clusterSpec.Spec.Network.DualStack.Enabled, clusterSpec.Spec.Network.ClusterDomain)
	defaultProfile.CgroupsPerQOS = boolPtr(true)
	defaultProfile.ResolverConfig = stringPtr("{{.ResolvConf}}")
	if err := k.writeConfigMapWithProfile(manifest, "default", defaultProfile); err != nil {
		return nil, fmt.Errorf("failed to write manifest for default profile config map: %w", err)
	}

	winDefaultProfile := newWorkerProfile(dnsAddress, clusterSpec.Spec.Network.DualStack.Enabled, clusterSpec.Spec.Network.ClusterDomain)
	winDefaultProfile.CgroupsPerQOS = boolPtr(false)
	if err := k.writeConfigMapWithProfile(manifest, "default-windows", winDefaultProfile); err != nil {
		return nil, fmt.Errorf("failed to write manifest for default-windows profile config map: %w", err)
	}

	configMapNames := []string{
		formatProfileName("default"),
		formatProfileName("default-windows"),
	}

	for _, profileSpec := range clusterSpec.Spec.WorkerProfiles {
		profile := newWorkerProfile(dnsAddress, false, clusterSpec.Spec.Network.ClusterDomain) // Do not add dualstack feature gate to the custom profiles

		// profile, err := patchedProfile(profile, profileSpec.Config)
		// if err != nil {
		// 	return nil, fmt.Errorf("failed to generate worker profile %q: %w", profileSpec.Name, err)
		// }

		var x kubeletconfig.KubeletConfiguration
		yaml.Unmarshal(profileSpec.Config, &x)
		if err != nil {
			return nil, err
		}

		err := patchProfile(profile, x)
		if err != nil {
			return nil, fmt.Errorf("failed to generate worker profile %q: %w", profileSpec.Name, err)
		}

		// FIXME the above should go away

		if err := k.writeConfigMapWithProfile(manifest, profileSpec.Name, profile); err != nil {
			return nil, fmt.Errorf("failed to write manifest for worker profile %q: %w", profileSpec.Name, err)
		}
		configMapNames = append(configMapNames, formatProfileName(profileSpec.Name))
	}
	if err := k.writeRbacRoleBindings(manifest, configMapNames); err != nil {
		return nil, fmt.Errorf("failed to write manifest for RBAC bindings: %w", err)
	}
	return manifest, nil
}

func (k *KubeletConfig) save(data []byte) error {
	kubeletDir := path.Join(k.k0sVars.ManifestsDir, "kubelet")
	err := dir.Init(kubeletDir, constant.ManifestsDirMode)
	if err != nil {
		return err
	}

	filePath := filepath.Join(kubeletDir, "kubelet-config.yaml")
	if err := os.WriteFile(filePath, data, constant.CertMode); err != nil {
		return fmt.Errorf("can't write kubelet configuration config map: %v", err)
	}
	return nil
}

func (k *KubeletConfig) writeConfigMapWithProfile(w io.Writer, name string, profile *workerProfile) error {
	profileYaml, err := yaml.Marshal(profile)
	if err != nil {
		return err
	}
	tw := templatewriter.TemplateWriter{
		Name:     "kubelet-config",
		Template: kubeletConfigsManifestTemplate,
		Data: struct {
			Name              string
			KubeletConfigYAML string
		}{
			Name:              formatProfileName(name),
			KubeletConfigYAML: string(profileYaml),
		},
	}
	return tw.WriteToBuffer(w)
}

func formatProfileName(name string) string {
	return fmt.Sprintf("kubelet-config-%s-%s", name, constant.KubernetesMajorMinorVersion)
}

func (k *KubeletConfig) writeRbacRoleBindings(w io.Writer, configMapNames []string) error {
	tw := templatewriter.TemplateWriter{
		Name:     "kubelet-config-rbac",
		Template: rbacRoleAndBindingsManifestTemplate,
		Data: struct {
			ConfigMapNames []string
		}{
			ConfigMapNames: configMapNames,
		},
	}

	return tw.WriteToBuffer(w)
}

func newWorkerProfile(dnsAddress string, dualStack bool, clusterDomain string) *workerProfile {
	// the motivation to keep it like this instead of the yaml template:
	// - it's easier to merge programatically defined structure
	// - apart from map[string]interface there is no good way to define free-form mapping

	profile := &workerProfile{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "kubelet.config.k8s.io/v1beta1",
			Kind:       "KubeletConfiguration",
		},
		Authentication: kubeletconfig.KubeletAuthentication{
			Anonymous: kubeletconfig.KubeletAnonymousAuthentication{
				Enabled: boolPtr(false),
			},
			Webhook: kubeletconfig.KubeletWebhookAuthentication{
				CacheTTL: zeroSecs,
				Enabled:  boolPtr(true),
			},
			X509: kubeletconfig.KubeletX509Authentication{
				// For the authentication.x509.clientCAFile and volumePluginDir
				// we want to use late binding so we put a template placeholder
				// instead of actual value there.
				ClientCAFile: "{{.ClientCAFile}}",
			},
		},
		Authorization: kubeletconfig.KubeletAuthorization{
			Mode: kubeletconfig.KubeletAuthorizationModeWebhook,
			Webhook: kubeletconfig.KubeletWebhookAuthorization{
				CacheAuthorizedTTL:   zeroSecs,
				CacheUnauthorizedTTL: zeroSecs,
			},
		},
		ClusterDNS:    []string{dnsAddress},
		ClusterDomain: clusterDomain,
		TLSCipherSuites: []string{
			"TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256",
			"TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256",
			"TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305",
			"TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384",
			"TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305",
			"TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384",
			"TLS_RSA_WITH_AES_256_GCM_SHA384",
			"TLS_RSA_WITH_AES_128_GCM_SHA256",
		},
		VolumeStatsAggPeriod: zeroSecs,
		VolumePluginDir:      "{{.VolumePluginDir}}", // see authentication.x509.clientCAFile
		FailSwapOn:           boolPtr(false),
		RotateCertificates:   true,
		ServerTLSBootstrap:   true,
		EventRecordQPS:       i32Ptr(0),
		KubeReservedCgroup:   "{{.KubeReservedCgroup}}",
		KubeletCgroups:       "{{.KubeletCgroups}}",
	}
	if dualStack {
		profile.FeatureGates = map[string]bool{
			"IPv6DualStack": true,
		}
	}
	return profile
}

const kubeletConfigsManifestTemplate = `---
apiVersion: v1
kind: ConfigMap
metadata:
  name: {{.Name}}
  namespace: kube-system
data:
  kubelet: |
{{ .KubeletConfigYAML | nindent 4 }}
`

const rbacRoleAndBindingsManifestTemplate = `---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: system:bootstrappers:kubelet-configmaps
  namespace: kube-system
rules:
- apiGroups: [""]
  resources: ["configmaps"]
  resourceNames:
{{- range .ConfigMapNames }}
    - "{{ . -}}"
{{ end }}
  verbs: ["get"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: system:bootstrappers:kubelet-configmaps
  namespace: kube-system
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: system:bootstrappers:kubelet-configmaps
subjects:
  - apiGroup: rbac.authorization.k8s.io
    kind: Group
    name: system:bootstrappers
  - apiGroup: rbac.authorization.k8s.io
    kind: Group
    name: system:nodes
`

// patchProfile merges b into a, a is modified in-place
func patchProfile(base *workerProfile, overrides workerProfile) error {
	// FIXME want to replace this with strategic merge patch below...
	return mergo.Merge(base, overrides, mergo.WithOverride)
}

// patchedProfile merges b into a, a is modified in-place
func patchedProfileX(base *workerProfile, overrides []byte) (*kubeletconfig.KubeletConfiguration, error) {
	original, err := json.Marshal(base)
	if err != nil {
		return nil, err
	}

	patch, err := yaml.YAMLToJSON(overrides)
	if err != nil {
		return nil, err
	}

	var result kubeletconfig.KubeletConfiguration
	patched, err := strategicpatch.StrategicMergePatch(original, patch, &result)
	if err != nil {
		return nil, err
	}

	err = yaml.Unmarshal(patched, &result)
	if err != nil {
		return nil, err
	}

	return &result, nil
}

// Health-check interface
func (k *KubeletConfig) Healthy() error { return nil }
