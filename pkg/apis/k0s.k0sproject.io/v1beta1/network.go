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

	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
	utilnet "k8s.io/utils/net"
)

// Network defines the network related config options
type Network struct {
	Calico     *Calico     `json:"calico"`
	DualStack  DualStack   `json:"dualStack,omitempty"`
	KubeProxy  *KubeProxy  `json:"kubeProxy"`
	KubeRouter *KubeRouter `json:"kuberouter"`

	// Pod network CIDR to use in the cluster
	PodCIDR string `json:"podCIDR"`
	// Network provider (valid values: calico, kuberouter, or custom)
	Provider string `json:"provider"`
	// Network CIDR to use for cluster VIP services
	ServiceCIDR string `json:"serviceCIDR,omitempty"`
	// Cluster Domain
	ClusterDomain string `json:"clusterDomain,omitempty"`
}

// IPPort represents a port in an IP network.
// +kubebuilder:validation:Minimum=1
// +kubebuilder:validation:Maximum=65535
type IPPort uint16

// Or returns this port or default_ if this port is zero.
func (p IPPort) Or(default_ uint16) IPPort {
	if p == 0 {
		return IPPort(default_)
	}
	return p
}

func (p IPPort) Validate(path *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}
	for _, detail := range validation.IsValidPortNum(int(p)) {
		allErrs = append(allErrs, field.Invalid(path, int32(p), detail))
	}
	return allErrs
}

func (p IPPort) ValidateOptional(path *field.Path) field.ErrorList {
	if p == 0 {
		return field.ErrorList{}
	}
	return p.Validate(path)
}

// DefaultNetwork creates the Network config struct with sane default values
func DefaultNetwork() *Network {
	return &Network{
		PodCIDR:       "10.244.0.0/16",
		ServiceCIDR:   "10.96.0.0/12",
		Provider:      "kuberouter",
		KubeRouter:    DefaultKubeRouter(),
		DualStack:     DefaultDualStack(),
		KubeProxy:     DefaultKubeProxy(),
		ClusterDomain: "cluster.local",
	}
}

// Validate validates all the settings make sense and should work
func (n *Network) Validate() []error {
	var errors []error
	if n.Provider != "calico" && n.Provider != "custom" && n.Provider != "kuberouter" {
		errors = append(errors, fmt.Errorf("unsupported provider: %q", n.Provider))
	}

	_, _, err := net.ParseCIDR(n.PodCIDR)
	if err != nil {
		errors = append(errors, fmt.Errorf("invalid pod CIDR %q", n.PodCIDR))
	}

	if n.ServiceCIDR != "" {
		_, _, err = net.ParseCIDR(n.ServiceCIDR)
		if err != nil {
			errors = append(errors, fmt.Errorf("invalid service CIDR %q", n.ServiceCIDR))
		}
	}

	for _, detail := range validation.IsDNS1123Subdomain(n.ClusterDomain) {
		errors = append(errors, field.Invalid(field.NewPath("clusterDomain"), n.ClusterDomain, detail))
	}

	if n.DualStack.Enabled {
		if n.Provider == "calico" && n.Calico.Mode != "bird" {
			errors = append(errors, fmt.Errorf("network dual stack is supported only for calico mode `bird`"))
		}
		_, _, err := net.ParseCIDR(n.DualStack.IPv6PodCIDR)
		if err != nil {
			errors = append(errors, fmt.Errorf("invalid pod IPv6 CIDR %q", n.DualStack.IPv6PodCIDR))
		}
		_, _, err = net.ParseCIDR(n.DualStack.IPv6ServiceCIDR)
		if err != nil {
			errors = append(errors, fmt.Errorf("invalid service IPv6 CIDR %q", n.DualStack.IPv6ServiceCIDR))
		}
	}

	path := field.NewPath("spec").Child("network")
	for name, v := range map[string]interface {
		Validate(*field.Path) field.ErrorList
	}{
		"kubeProxy": n.KubeProxy,
	} {
		for _, err := range v.Validate(path.Child(name)) {
			errors = append(errors, err)
		}
	}

	return errors
}

// DNSAddress calculates the 10th address of configured service CIDR block.
func (n *Network) DNSAddress() (string, error) {
	_, ipnet, err := net.ParseCIDR(n.ServiceCIDR)
	if err != nil {
		return "", fmt.Errorf("failed to parse service CIDR %q: %w", n.ServiceCIDR, err)
	}

	address := ipnet.IP.To4()
	if IsIPv6String(ipnet.IP.String()) {
		address = ipnet.IP.To16()
	}

	prefixlen, _ := ipnet.Mask.Size()
	if prefixlen < 29 {
		address[3] = address[3] + 10
	} else {
		address[3] = address[3] + 2
	}

	if !ipnet.Contains(address) {
		return "", fmt.Errorf("failed to calculate a valid DNS address: %q", address.String())
	}

	return address.String(), nil
}

// InternalAPIAddresses calculates the internal API address of configured service CIDR block.
func (n *Network) InternalAPIAddresses() ([]string, error) {
	cidrs := []string{n.ServiceCIDR}

	if n.DualStack.Enabled {
		cidrs = append(cidrs, n.DualStack.IPv6ServiceCIDR)
	}

	parsedCIDRs, err := utilnet.ParseCIDRs(cidrs)
	if err != nil {
		return nil, fmt.Errorf("can't parse service cidr to build internal API address: %w", err)
	}

	stringifiedAddresses := make([]string, len(parsedCIDRs))
	for i, ip := range parsedCIDRs {
		apiIP, err := utilnet.GetIndexedIP(ip, 1)
		if err != nil {
			return nil, fmt.Errorf("can't build internal API address: %v", err)
		}
		stringifiedAddresses[i] = apiIP.String()
	}
	return stringifiedAddresses, nil
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

// BuildServiceCIDR returns actual argument value for service cidr
func (n *Network) BuildServiceCIDR(addr string) string {
	if !n.DualStack.Enabled {
		return n.ServiceCIDR
	}
	// because in the dual-stack mode k8s
	// relies on the ordering of the given CIDRs
	// we need to first give family on which
	// api server listens
	if IsIPv6String(addr) {
		return n.DualStack.IPv6ServiceCIDR + "," + n.ServiceCIDR
	}
	return n.ServiceCIDR + "," + n.DualStack.IPv6ServiceCIDR
}

// BuildPodCIDR returns actual argument value for pod cidr
func (n *Network) BuildPodCIDR() string {
	if n.DualStack.Enabled {
		return n.DualStack.IPv6PodCIDR + "," + n.PodCIDR
	}
	return n.PodCIDR
}
