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
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/k0sproject/k0s/internal/testutil"
	config "github.com/k0sproject/k0s/pkg/apis/k0s.k0sproject.io/v1beta1"
	"github.com/k0sproject/k0s/pkg/constant"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/kubelet/config/v1beta1"
	"sigs.k8s.io/yaml"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var k0sVars = constant.GetConfig("")

var expectedDefaultProfileConfigMap = `
apiVersion: v1
kind: ConfigMap
metadata:
  name: kubelet-config-default-###VERSION###
  namespace: kube-system
data:
  kubelet: |

    apiVersion: kubelet.config.k8s.io/v1beta1
    authentication:
      anonymous:
        enabled: false
      webhook:
        cacheTTL: 0s
        enabled: true
      x509:
        clientCAFile: '{{.ClientCAFile}}'
    authorization:
      mode: Webhook
      webhook:
        cacheAuthorizedTTL: 0s
        cacheUnauthorizedTTL: 0s
    cgroupsPerQOS: true
    clusterDNS:
    - 10.96.0.10
    clusterDomain: cluster.local
    cpuManagerReconcilePeriod: 0s
    eventRecordQPS: 0
    evictionPressureTransitionPeriod: 0s
    failSwapOn: false
    fileCheckFrequency: 0s
    httpCheckFrequency: 0s
    imageMinimumGCAge: 0s
    kind: KubeletConfiguration
    kubeReservedCgroup: '{{.KubeReservedCgroup}}'
    kubeletCgroups: '{{.KubeletCgroups}}'
    logging:
      flushFrequency: 0
      options:
        json:
          infoBufferSize: "0"
      verbosity: 0
    memorySwap: {}
    nodeStatusReportFrequency: 0s
    nodeStatusUpdateFrequency: 0s
    resolvConf: '{{.ResolvConf}}'
    rotateCertificates: true
    runtimeRequestTimeout: 0s
    serverTLSBootstrap: true
    shutdownGracePeriod: 0s
    shutdownGracePeriodCriticalPods: 0s
    streamingConnectionIdleTimeout: 0s
    syncFrequency: 0s
    tlsCipherSuites:
    - TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256
    - TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256
    - TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305
    - TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384
    - TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305
    - TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384
    - TLS_RSA_WITH_AES_256_GCM_SHA384
    - TLS_RSA_WITH_AES_128_GCM_SHA256
    volumePluginDir: '{{.VolumePluginDir}}'
    volumeStatsAggPeriod: 0s
    
`

func Test_KubeletConfig(t *testing.T) {
	dnsAddr, _ := cfg.Spec.Network.DNSAddress()
	t.Run("default_profile_only", func(t *testing.T) {
		k, err := NewKubeletConfig(k0sVars, testutil.NewFakeClientFactory())
		require.NoError(t, err)

		t.Log("starting to run...")
		buf, err := k.createProfiles(cfg)
		require.NoError(t, err)
		if err != nil {
			t.FailNow()
		}
		manifestYamls := strings.Split(strings.TrimSuffix(buf.String(), "---"), "---")[1:]
		t.Run("output_must_have_3_manifests", func(t *testing.T) {
			require.Len(t, manifestYamls, 4, "Must have exactly 4 generated manifests per profile")
			requireConfigMap(t, manifestYamls[0], "kubelet-config-default-1.23")
			requireConfigMap(t, manifestYamls[1], "kubelet-config-default-windows-1.23")
			requireRole(t, manifestYamls[2], []string{
				formatProfileName("default"),
				formatProfileName("default-windows"),
			})
			requireRoleBinding(t, manifestYamls[3])
		})
		t.Run("renders_correctly", func(t *testing.T) {
			expected := strings.ReplaceAll(expectedDefaultProfileConfigMap, "###VERSION###", constant.KubernetesMajorMinorVersion)
			assert.Equal(t, expected, manifestYamls[0])
		})
	})
	t.Run("default_profile_must_have_feature_gates_if_dualstack_setup", func(t *testing.T) {
		profile := newWorkerProfile(dnsAddr, true, "cluster.local")
		require.Equal(t, map[string]bool{
			"IPv6DualStack": true,
		}, profile.FeatureGates)
	})
	t.Run("default_profile_must_pass_down_cluster_domain", func(t *testing.T) {
		profile := newWorkerProfile(dnsAddr, true, "cluster.local.custom")
		require.Equal(t, string(
			"cluster.local.custom",
		), profile.ClusterDomain)
	})
	t.Run("with_user_provided_profiles", func(t *testing.T) {
		k := defaultConfigWithUserProvidedProfiles(t)
		buf, err := k.createProfiles(cfg)
		require.NoError(t, err)
		manifestYamls := strings.Split(strings.TrimSuffix(buf.String(), "---"), "---")[1:]
		expectedManifestsCount := 6
		require.Len(t, manifestYamls, expectedManifestsCount, "Must have exactly 3 generated manifests per profile")

		t.Run("final_output_must_have_manifests_for_profiles", func(t *testing.T) {
			// check that each profile has config map, role and role binding
			var resourceNamesForRole []string
			for idx, profileName := range []string{"default", "default-windows", "profile_XXX", "profile_YYY"} {
				fullName := "kubelet-config-" + profileName + "-" + constant.KubernetesMajorMinorVersion
				resourceNamesForRole = append(resourceNamesForRole, formatProfileName(profileName))
				requireConfigMap(t, manifestYamls[idx], fullName)
			}
			requireRole(t, manifestYamls[len(resourceNamesForRole)], resourceNamesForRole)
		})
		t.Run("user_profile_X_must_be_merged_with_default_profile", func(t *testing.T) {
			profileXXX := corev1.ConfigMap{}
			profileYYY := corev1.ConfigMap{}

			require.NoError(t, yaml.Unmarshal([]byte(manifestYamls[2]), &profileXXX))
			require.NoError(t, yaml.Unmarshal([]byte(manifestYamls[3]), &profileYYY))

			kubeletXXX, ok := profileXXX.Data["kubelet"]
			assert.True(t, ok, "worker profile doesn't contain kubelet configuration")
			kubeletYYY, ok := profileYYY.Data["kubelet"]
			assert.True(t, ok, "worker profile doesn't contain kubelet configuration")

			// manually apply the same changes to default config and check that there is no diff
			defaultProfileKubeletConfig := newWorkerProfile(dnsAddr, false, "cluster.local")
			defaultProfileKubeletConfig.Authentication.Anonymous.Enabled = boolPtr(false)
			defaultWithChangesXXX, err := yaml.Marshal(defaultProfileKubeletConfig)
			require.NoError(t, err)

			defaultProfileKubeletConfig = newWorkerProfile(dnsAddr, false, "cluster.local")
			defaultProfileKubeletConfig.Authentication.Webhook.CacheTTL = metav1.Duration{15 * time.Second}
			defaultWithChangesYYY, err := yaml.Marshal(defaultProfileKubeletConfig)

			require.NoError(t, err)

			assert.YAMLEq(t, string(defaultWithChangesXXX), kubeletXXX)
			assert.YAMLEq(t, string(defaultWithChangesYYY), kubeletYYY)
		})
	})
}

func defaultConfigWithUserProvidedProfiles(t *testing.T) *KubeletConfig {
	k, err := NewKubeletConfig(k0sVars, testutil.NewFakeClientFactory())
	require.NoError(t, err)

	cfgProfileX := workerProfile{
		Authentication: v1beta1.KubeletAuthentication{
			Anonymous: v1beta1.KubeletAnonymousAuthentication{
				Enabled: boolPtr(false),
			},
		},
	}

	wcx, err := yaml.Marshal(cfgProfileX)
	require.NoError(t, err)

	cfg.Spec.WorkerProfiles = append(cfg.Spec.WorkerProfiles,
		config.WorkerProfile{
			Name:   "profile_XXX",
			Config: wcx,
		},
	)

	cfgProfileY := workerProfile{
		Authentication: v1beta1.KubeletAuthentication{
			Webhook: v1beta1.KubeletWebhookAuthentication{
				CacheTTL: metav1.Duration{15 * time.Second},
			},
		},
	}

	wcy, err := json.Marshal(cfgProfileY)
	if err != nil {
		t.Fatal(err)
	}

	cfg.Spec.WorkerProfiles = append(cfg.Spec.WorkerProfiles,
		config.WorkerProfile{
			Name:   "profile_YYY",
			Config: wcy,
		},
	)
	return k
}

func requireConfigMap(t *testing.T, spec string, name string) {
	dst := corev1.ConfigMap{}
	require.NoError(t, yaml.Unmarshal([]byte(spec), &dst))
	require.Equal(t, "ConfigMap", dst.TypeMeta.Kind)
	require.Equal(t, name, dst.ObjectMeta.Name)
	spec, foundSpec := dst.Data["kubelet"]
	require.True(t, foundSpec, "kubelet config map must have embedded kubelet config")
	require.True(t, strings.TrimSpace(spec) != "", "kubelet config map must have non-empty embedded kubelet config")
}

func requireRole(t *testing.T, spec string, expectedResourceNames []string) {
	dst := rbacv1.Role{}
	require.NoError(t, yaml.Unmarshal([]byte(spec), &dst))
	require.Equal(t, "Role", dst.TypeMeta.Kind)
	require.Equal(t, "system:bootstrappers:kubelet-configmaps", dst.ObjectMeta.Name)
	require.Equal(t, expectedResourceNames, dst.Rules[0].ResourceNames)
}

func requireRoleBinding(t *testing.T, spec string) {
	dst := rbacv1.RoleBinding{}
	require.NoError(t, yaml.Unmarshal([]byte(spec), &dst))
	require.Equal(t, "RoleBinding", dst.TypeMeta.Kind)
	require.Equal(t, "system:bootstrappers:kubelet-configmaps", dst.ObjectMeta.Name)
}
