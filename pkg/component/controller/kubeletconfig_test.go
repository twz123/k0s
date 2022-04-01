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

	"github.com/k0sproject/k0s/internal/testutil"
	"github.com/k0sproject/k0s/pkg/apis/helm.k0sproject.io/v1beta1"
	config "github.com/k0sproject/k0s/pkg/apis/k0s.k0sproject.io/v1beta1"
	"github.com/k0sproject/k0s/pkg/constant"
	"gopkg.in/yaml.v2"

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
    eventRecordQPS: 0
    failSwapOn: false
    kind: KubeletConfiguration
    kubeReservedCgroup: '{{.KubeReservedCgroup}}'
    kubeletCgroups: '{{.KubeletCgroups}}'
    resolvConf: '{{.ResolvConf}}'
    rotateCertificates: true
    serverTLSBootstrap: true
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
		profile := getDefaultProfile(dnsAddr, true, "cluster.local")
		require.Equal(t, map[string]bool{
			"IPv6DualStack": true,
		}, profile["featureGates"])
	})
	t.Run("default_profile_must_pass_down_cluster_domain", func(t *testing.T) {
		profile := getDefaultProfile(dnsAddr, true, "cluster.local.custom")
		require.Equal(t, string(
			"cluster.local.custom",
		), profile["clusterDomain"])
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
			profileXXX := struct {
				Data map[string]string `yaml:"data"`
			}{}

			profileYYY := struct {
				Data map[string]string `yaml:"data"`
			}{}

			require.NoError(t, yaml.Unmarshal([]byte(manifestYamls[2]), &profileXXX))
			require.NoError(t, yaml.Unmarshal([]byte(manifestYamls[3]), &profileYYY))

			kubeletXXX, ok := profileXXX.Data["kubelet"]
			assert.True(t, ok, "worker profile doesn't contain kubelet configuration")
			kubeletYYY, ok := profileYYY.Data["kubelet"]
			assert.True(t, ok, "worker profile doesn't contain kubelet configuration")

			// manually apply the same changes to default config and check that there is no diff
			defaultProfileKubeletConfig := getDefaultProfile(dnsAddr, false, "cluster.local")
			defaultProfileKubeletConfig["authentication"].(map[string]interface{})["anonymous"].(map[string]interface{})["enabled"] = false
			defaultWithChangesXXX, err := yaml.Marshal(defaultProfileKubeletConfig)
			require.NoError(t, err)

			defaultProfileKubeletConfig = getDefaultProfile(dnsAddr, false, "cluster.local")
			defaultProfileKubeletConfig["authentication"].(map[string]interface{})["webhook"].(map[string]interface{})["cacheTTL"] = "15s"
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

	cfgProfileX := map[string]interface{}{
		"authentication": map[string]interface{}{
			"anonymous": map[string]interface{}{
				"enabled": false,
			},
		},
	}
	wcx, err := json.Marshal(cfgProfileX)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Spec.WorkerProfiles = append(cfg.Spec.WorkerProfiles,
		config.WorkerProfile{
			Name:   "profile_XXX",
			Config: wcx,
		},
	)

	cfgProfileY := map[string]interface{}{
		"authentication": map[string]interface{}{
			"webhook": map[string]interface{}{
				"cacheTTL": "15s",
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
	dst := map[string]interface{}{}
	require.NoError(t, yaml.Unmarshal([]byte(spec), &dst))
	dst = v1beta1.CleanUpGenericMap(dst)
	require.Equal(t, "ConfigMap", dst["kind"])
	require.Equal(t, name, dst["metadata"].(map[string]interface{})["name"])
	spec, foundSpec := dst["data"].(map[string]interface{})["kubelet"].(string)
	require.True(t, foundSpec, "kubelet config map must have embedded kubelet config")
	require.True(t, strings.TrimSpace(spec) != "", "kubelet config map must have non-empty embedded kubelet config")
}

func requireRole(t *testing.T, spec string, expectedResourceNames []string) {
	dst := map[string]interface{}{}
	require.NoError(t, yaml.Unmarshal([]byte(spec), &dst))
	dst = v1beta1.CleanUpGenericMap(dst)
	require.Equal(t, "Role", dst["kind"])
	require.Equal(t, "system:bootstrappers:kubelet-configmaps", dst["metadata"].(map[string]interface{})["name"])
	var currentResourceNames []string
	for _, el := range dst["rules"].([]interface{})[0].(map[string]interface{})["resourceNames"].([]interface{}) {
		currentResourceNames = append(currentResourceNames, el.(string))
	}
	require.Equal(t, expectedResourceNames, currentResourceNames)
}

func requireRoleBinding(t *testing.T, spec string) {
	dst := map[string]interface{}{}
	require.NoError(t, yaml.Unmarshal([]byte(spec), &dst))
	dst = v1beta1.CleanUpGenericMap(dst)
	require.Equal(t, "RoleBinding", dst["kind"])
	require.Equal(t, "system:bootstrappers:kubelet-configmaps", dst["metadata"].(map[string]interface{})["name"])
}
