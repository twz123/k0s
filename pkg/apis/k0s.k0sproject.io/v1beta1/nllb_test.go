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

package v1beta1

import (
	"fmt"
	"testing"

	"k8s.io/utils/pointer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNodeLocalLoadBalancer_IsEnabled(t *testing.T) {
	for _, test := range []struct {
		name    string
		enabled bool
		nllb    *NodeLocalLoadBalancer
	}{
		{"nil", false, nil},
		{"default", true, &NodeLocalLoadBalancer{}},
		{"true", true, &NodeLocalLoadBalancer{Enabled: pointer.Bool(true)}},
		{"false", false, &NodeLocalLoadBalancer{Enabled: pointer.Bool(false)}},
	} {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.enabled, test.nllb.IsEnabled())
		})
	}
}

func TestNodeLocalLoadBalancer_Unmarshal_ImageOverride(t *testing.T) {
	yamlData := `
apiVersion: k0s.k0sproject.io/v1beta1
kind: ClusterConfig
metadata:
  name: foobar
spec:
  images:
    repository: example.com
`

	c, err := ConfigFromString(yamlData)
	require.NoError(t, err)
	errors := c.Validate()
	require.Nil(t, errors)

	nllb := c.Spec.Network.NodeLocalLoadBalancer
	require.NotNil(t, nllb.EnvoyProxy)
	require.NotNil(t, nllb.EnvoyProxy.Image)
	require.Contains(t, nllb.EnvoyProxy.Image.Image, "example.com/")
}

func TestEnvoyProxyImage_Validate(t *testing.T) {
	yamlData := `
apiVersion: k0s.k0sproject.io/v1beta1
kind: ClusterConfig
metadata:
  name: foobar
spec:
  network:
    nodeLocalLoadBalancer:
      envoyProxy:
        image:
          %s: ""
`

	for _, test := range []struct {
		field, expectedMsg string
	}{
		{"image", "network: nodeLocalLoadBalancer.envoyProxy.image.image: Required value"},
		{"version", `network: nodeLocalLoadBalancer.envoyProxy.image.version: Invalid value: "": must match regular expression: ^[\w][\w.-]{0,127}$`},
	} {
		t.Run(test.field, func(t *testing.T) {
			c, err := ConfigFromString(fmt.Sprintf(yamlData, test.field))
			require.NoError(t, err)
			errors := c.Validate()
			require.Len(t, errors, 1)
			assert.Equal(t, test.expectedMsg, errors[0].Error())
		})
	}
}
