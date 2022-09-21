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
	"testing"

	"github.com/stretchr/testify/suite"
)

type NetworkSuite struct {
	suite.Suite
}

func (s *NetworkSuite) TestDomainMarshaling() {
	yamlData := `
spec:
  storage:
    type: kine
  network:
    clusterDomain: something.local
`
	c, err := ConfigFromString(yamlData)
	s.NoError(err)
	n := c.Spec.Network
	s.Equal("kuberouter", n.Provider)
	s.NotNil(n.KubeRouter)
	s.Equal("something.local", n.ClusterDomain)
}

func (s *NetworkSuite) TestNetworkDefaults() {
	n := DefaultNetwork()

	s.Equal("kuberouter", n.Provider)
	s.NotNil(n.KubeRouter)
	s.Equal(ModeIptables, n.KubeProxy.Mode)
	s.Equal("cluster.local", n.ClusterDomain)
}

func (s *NetworkSuite) TestCalicoDefaultsAfterMashaling() {
	yamlData := `
apiVersion: k0s.k0sproject.io/v1beta1
kind: ClusterConfig
metadata:
  name: foobar
spec:
  network:
    provider: calico
    calico:
`

	c, err := ConfigFromString(yamlData)
	s.NoError(err)
	n := c.Spec.Network

	s.Equal("calico", n.Provider)
	s.NotNil(n.Calico)
	s.Equal(4789, n.Calico.VxlanPort)
	s.Equal(0, n.Calico.MTU)
	s.Equal("vxlan", n.Calico.Mode)
}

func (s *NetworkSuite) TestKubeRouterDefaultsAfterMashaling() {
	yamlData := `
apiVersion: k0s.k0sproject.io/v1beta1
kind: ClusterConfig
metadata:
  name: foobar
spec:
  network:
    provider: kuberouter
    kuberouter:
`

	c, err := ConfigFromString(yamlData)
	s.NoError(err)
	n := c.Spec.Network

	s.Equal("kuberouter", n.Provider)
	s.NotNil(n.KubeRouter)
	s.Nil(n.Calico)

	s.True(n.KubeRouter.AutoMTU)
	s.Equal(0, n.KubeRouter.MTU)
	s.Empty(n.KubeRouter.PeerRouterASNs)
	s.Empty(n.KubeRouter.PeerRouterIPs)
}

func (s *NetworkSuite) TestKubeProxyDefaultsAfterMashaling() {
	yamlData := `
apiVersion: k0s.k0sproject.io/v1beta1
kind: ClusterConfig
metadata:
  name: foobar
spec:
`

	c, err := ConfigFromString(yamlData)
	s.NoError(err)
	p := c.Spec.Network.KubeProxy

	s.Equal(ModeIptables, p.Mode)
	s.False(p.Disabled)
}

func (s *NetworkSuite) TestKubeProxyDisabling() {
	yamlData := `
apiVersion: k0s.k0sproject.io/v1beta1
kind: ClusterConfig
metadata:
  name: foobar
spec:
  network:
    kubeProxy:
      disabled: true
`

	c, err := ConfigFromString(yamlData)
	s.NoError(err)
	p := c.Spec.Network.KubeProxy

	s.True(p.Disabled)
}

func (s *NetworkSuite) TestValidation() {
	s.Run("defaults_are_valid", func() {
		n := DefaultNetwork()

		s.Nil(n.Validate())
	})

	s.Run("invalid_provider", func() {
		n := DefaultNetwork()
		n.Provider = "foobar"

		errors := n.Validate()
		s.NotNil(errors)
		s.Len(errors, 1)
	})

	s.Run("invalid_pod_cidr", func() {
		n := DefaultNetwork()
		n.PodCIDR = "foobar"

		errors := n.Validate()
		s.NotNil(errors)
		s.Len(errors, 1)
		s.Contains(errors[0].Error(), "invalid pod CIDR")
	})

	s.Run("invalid_service_cidr", func() {
		n := DefaultNetwork()
		n.ServiceCIDR = "foobar"

		errors := n.Validate()
		s.NotNil(errors)
		s.Len(errors, 1)
		s.Contains(errors[0].Error(), "invalid service CIDR")
	})

	s.Run("invalid_cluster_domain", func() {
		n := DefaultNetwork()
		n.ClusterDomain = ".invalid-cluster-domain"

		errors := n.Validate()
		s.NotNil(errors)
		s.Len(errors, 1)
		s.Contains(errors[0].Error(), "invalid clusterDomain .invalid-cluster-domain")
	})

	s.Run("invalid_ipv6_service_cidr", func() {
		n := DefaultNetwork()
		n.Calico = DefaultCalico()
		n.Calico.Mode = "bird"
		n.KubeProxy.Mode = "ipvs"
		n.DualStack = &DualStack{
			Enabled:         true,
			IPv6ServiceCIDR: "foobar",
			IPv6PodCIDR:     "fd00::/108",
		}

		errors := n.Validate()
		s.NotNil(errors)
		s.Len(errors, 1)
		s.Contains(errors[0].Error(), "invalid service IPv6 CIDR")
	})

	s.Run("invalid_ipv6_pod_cidr", func() {
		n := DefaultNetwork()
		n.Calico = DefaultCalico()
		n.Calico.Mode = "bird"
		n.DualStack = &DualStack{
			Enabled:         true,
			IPv6ServiceCIDR: "fd00::/108",
			IPv6PodCIDR:     "foobar",
		}
		n.KubeProxy.Mode = "ipvs"

		errors := n.Validate()
		s.NotNil(errors)
		s.Len(errors, 1)
		s.Contains(errors[0].Error(), "invalid pod IPv6 CIDR")
	})

	s.Run("invalid_mode_for_kube_proxy", func() {
		n := DefaultNetwork()
		n.KubeProxy.Mode = "foobar"

		errors := n.Validate()
		s.NotNil(errors)
		s.Len(errors, 1)
		s.Contains(errors[0].Error(), "unsupported mode")
	})

	s.Run("valid_proxy_disabled_for_dualstack", func() {
		n := DefaultNetwork()
		n.Calico = DefaultCalico()
		n.Calico.Mode = "bird"
		n.DualStack = &DualStack{
			Enabled:         true,
			IPv6ServiceCIDR: "fd01::/108",
			IPv6PodCIDR:     "fd00::/108",
		}
		n.KubeProxy.Disabled = true
		n.KubeProxy.Mode = "iptables"

		errors := n.Validate()
		s.Nil(errors)
	})
}

func TestNetworkSuite(t *testing.T) {
	ns := &NetworkSuite{}

	suite.Run(t, ns)
}
