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
	"net"
	"strings"
	"testing"

	"github.com/k0sproject/k0s/internal/testutil"
	helmv1beta1 "github.com/k0sproject/k0s/pkg/apis/helm.k0sproject.io/v1beta1"
	"github.com/k0sproject/k0s/pkg/apis/k0s.k0sproject.io/v1beta1"
	"github.com/k0sproject/k0s/pkg/config"
	"github.com/k0sproject/k0s/pkg/constant"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"
)

var k0sVars = constant.GetConfig("")

func Test_KubeletConfig(t *testing.T) {
	clusterDNSIPs := []string{"127.10.10.10"}
	network := config.ControlPlaneNetworkSpec{
		ClusterDomain: "test.k0s.local",
		ServiceCIDRs: config.CIDRSpec{
			V4: &net.IPNet{IP: net.IP{127, 10, 10, 0}, Mask: net.CIDRMask(24, net.IPv4len*8)},
		},
	}

	t.Run("default_profile_only", func(t *testing.T) {
		k, err := NewKubeletConfig(&k0sVars, &network, testutil.NewFakeClientFactory())
		require.NoError(t, err)

		t.Log("starting to run...")
		buf, err := k.createProfiles(nil)
		require.NoError(t, err)
		if err != nil {
			t.FailNow()
		}
		manifestYamls := strings.Split(strings.TrimSuffix(buf.String(), "---"), "---")[1:]
		t.Run("output_must_have_3_manifests", func(t *testing.T) {
			require.Len(t, manifestYamls, 4, "Must have exactly 4 generated manifests per profile")
			requireConfigMap(t, manifestYamls[0], "kubelet-config-default-1.25")
			requireConfigMap(t, manifestYamls[1], "kubelet-config-default-windows-1.25")
			requireRole(t, manifestYamls[2], []string{
				formatProfileName("default"),
				formatProfileName("default-windows"),
			})
			requireRoleBinding(t, manifestYamls[3])
		})
	})
	t.Run("default_profile_must_pass_down_cluster_domain", func(t *testing.T) {
		profile := getDefaultProfile(network.ClusterDomain, clusterDNSIPs)
		require.Equal(t, network.ClusterDomain, profile["clusterDomain"])
	})
	t.Run("with_user_provided_profiles", func(t *testing.T) {
		workerProfiles := v1beta1.WorkerProfiles{
			makeWorkerProfile(t, "profile_XXX", map[string]interface{}{
				"authentication": map[string]interface{}{
					"anonymous": map[string]interface{}{
						"enabled": false,
					},
				},
			}),
			makeWorkerProfile(t, "profile_YYY", map[string]interface{}{
				"authentication": map[string]interface{}{
					"webhook": map[string]interface{}{
						"cacheTTL": "15s",
					},
				},
			}),
		}

		k, err := NewKubeletConfig(&k0sVars, &network, testutil.NewFakeClientFactory())
		require.NoError(t, err)
		buf, err := k.createProfiles(workerProfiles)
		require.NoError(t, err)
		manifestYamls := strings.Split(strings.TrimSuffix(buf.String(), "---"), "---")[1:]
		expectedManifestsCount := 6
		require.Len(t, manifestYamls, expectedManifestsCount, "Must have exactly 3 generated manifests per profile")

		t.Run("final_output_must_have_manifests_for_profiles", func(t *testing.T) {
			// check that each profile has config map, role and role binding
			var resourceNamesForRole []string
			for idx, profileName := range []string{"default", "default-windows", "profile_XXX", "profile_YYY"} {
				fullName := "kubelet-config-" + profileName + "-1.25"
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

			// manually apple the same changes to default config and check that there is no diff
			defaultProfileKubeletConfig := getDefaultProfile(network.ClusterDomain, clusterDNSIPs)
			defaultProfileKubeletConfig["authentication"] = map[string]interface{}{
				"anonymous": map[string]interface{}{
					"enabled": false,
				},
			}
			defaultWithChangesXXX, err := yaml.Marshal(defaultProfileKubeletConfig)
			require.NoError(t, err)

			defaultProfileKubeletConfig = getDefaultProfile(network.ClusterDomain, clusterDNSIPs)
			defaultProfileKubeletConfig["authentication"] = map[string]interface{}{
				"webhook": map[string]interface{}{
					"cacheTTL": "15s",
				},
			}
			defaultWithChangesYYY, err := yaml.Marshal(defaultProfileKubeletConfig)

			require.NoError(t, err)

			require.YAMLEq(t, string(defaultWithChangesXXX), profileXXX.Data["kubelet"])
			require.YAMLEq(t, string(defaultWithChangesYYY), profileYYY.Data["kubelet"])
		})
	})
}

func makeWorkerProfile(t *testing.T, name string, profile map[string]interface{}) v1beta1.WorkerProfile {
	data, err := json.Marshal(profile)
	require.NoError(t, err)
	return v1beta1.WorkerProfile{Name: name, Config: data}
}

func requireConfigMap(t *testing.T, spec string, name string) {
	dst := map[string]interface{}{}
	require.NoError(t, yaml.Unmarshal([]byte(spec), &dst))
	dst = helmv1beta1.CleanUpGenericMap(dst)
	require.Equal(t, "ConfigMap", dst["kind"])
	assert.Equal(t, name, dst["metadata"].(map[string]interface{})["name"])
	spec, foundSpec := dst["data"].(map[string]interface{})["kubelet"].(string)
	require.True(t, foundSpec, "kubelet config map must have embedded kubelet config")
	require.True(t, strings.TrimSpace(spec) != "", "kubelet config map must have non-empty embedded kubelet config")
}

func requireRole(t *testing.T, spec string, expectedResourceNames []string) {
	dst := map[string]interface{}{}
	require.NoError(t, yaml.Unmarshal([]byte(spec), &dst))
	dst = helmv1beta1.CleanUpGenericMap(dst)
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
	dst = helmv1beta1.CleanUpGenericMap(dst)
	require.Equal(t, "RoleBinding", dst["kind"])
	require.Equal(t, "system:bootstrappers:kubelet-configmaps", dst["metadata"].(map[string]interface{})["name"])
}
