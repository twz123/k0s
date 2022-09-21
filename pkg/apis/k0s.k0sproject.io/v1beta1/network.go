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
	"encoding/json"
	"fmt"
	"net"

	utilnet "k8s.io/utils/net"

	"github.com/asaskevich/govalidator"
)

var _ Validateable = (*Network)(nil)

// Network defines the network related config options
type Network struct {
	Calico     *Calico     `json:"calico,omitempty"`
	DualStack  *DualStack  `json:"dualStack,omitempty"`
	KubeProxy  *KubeProxy  `json:"kubeProxy,omitempty"`
	KubeRouter *KubeRouter `json:"kuberouter,omitempty"`

	// Pod network CIDR to use in the cluster
	PodCIDR string `json:"podCIDR"`
	// Network provider (valid values: calico, kuberouter, or custom)
	Provider string `json:"provider"`
	// Network CIDR to use for cluster VIP services
	ServiceCIDR string `json:"serviceCIDR,omitempty"`
	// Cluster Domain
	ClusterDomain string `json:"clusterDomain,omitempty"`
}

// DefaultNetwork creates the Network config struct with sane default values
func DefaultNetwork() *Network {
	return &Network{
		PodCIDR:       "10.244.0.0/16",
		ServiceCIDR:   "10.96.0.0/12",
		Provider:      "kuberouter",
		KubeRouter:    DefaultKubeRouter(),
		KubeProxy:     DefaultKubeProxy(),
		ClusterDomain: "cluster.local",
	}
}

// Validate validates all the settings make sense and should work
func (n *Network) Validate() []error {
	var errors []error
	if n.Provider != "calico" && n.Provider != "custom" && n.Provider != "kuberouter" {
		errors = append(errors, fmt.Errorf("unsupported network provider: %s", n.Provider))
	}

	_, _, err := net.ParseCIDR(n.PodCIDR)
	if err != nil {
		errors = append(errors, fmt.Errorf("invalid pod CIDR %s", n.PodCIDR))
	}

	_, _, err = net.ParseCIDR(n.ServiceCIDR)
	if err != nil {
		errors = append(errors, fmt.Errorf("invalid service CIDR %s", n.ServiceCIDR))
	}

	if !govalidator.IsDNSName(n.ClusterDomain) {
		errors = append(errors, fmt.Errorf("invalid clusterDomain %s", n.ClusterDomain))
	}

	if n.DualStack.IsEnabled() {
		if n.Provider == "calico" && n.Calico.Mode != "bird" {
			errors = append(errors, fmt.Errorf("network dual stack is supported only for calico mode `bird`"))
		}
		_, _, err := net.ParseCIDR(n.DualStack.IPv6PodCIDR)
		if err != nil {
			errors = append(errors, fmt.Errorf("invalid pod IPv6 CIDR %s", n.DualStack.IPv6PodCIDR))
		}
		_, _, err = net.ParseCIDR(n.DualStack.IPv6ServiceCIDR)
		if err != nil {
			errors = append(errors, fmt.Errorf("invalid service IPv6 CIDR %s", n.DualStack.IPv6ServiceCIDR))
		}
	}
	errors = append(errors, n.KubeProxy.Validate()...)
	return errors
}

// DNSAddress calculates the 10th address of configured service CIDR block.
func (n *Network) DNSAddress() (net.IP, error) {
	_, ipNet, err := net.ParseCIDR(n.ServiceCIDR)
	if err != nil {
		return nil, fmt.Errorf("failed to parse service CIDR %q", n.ServiceCIDR)
	}

	ip, err := utilnet.GetIndexedIP(ipNet, 10)
	if err != nil {
		ip, err = utilnet.GetIndexedIP(ipNet, 2)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to calculate a valid DNS address for service CIDR %s: %w", n.ServiceCIDR, err)
	}

	v4Addr := ip.To4()
	if v4Addr != nil {
		return v4Addr, nil
	}

	return ip, nil
}

// UnmarshalJSON sets in some sane defaults when unmarshaling the data from json
func (n *Network) UnmarshalJSON(data []byte) error {
	n.Provider = "kuberouter"

	type network Network
	jc := (*network)(n)

	if err := json.Unmarshal(data, jc); err != nil {
		return err
	}

	if n.Provider == "calico" && n.Calico == nil {
		n.Calico = DefaultCalico()
		n.KubeRouter = nil
	} else if n.Provider == "kuberouter" && n.KubeRouter == nil {
		n.KubeRouter = DefaultKubeRouter()
		n.Calico = nil
	}

	if n.KubeProxy == nil {
		n.KubeProxy = DefaultKubeProxy()
	}

	return nil
}

// BuildPodCIDR returns actual argument value for pod cidr
func (n *Network) BuildPodCIDR() string {
	if n.DualStack.IsEnabled() {
		return n.DualStack.IPv6PodCIDR + "," + n.PodCIDR
	}
	return n.PodCIDR
}
