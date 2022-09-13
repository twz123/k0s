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

import "encoding/json"

// Calico defines the calico related config options
type Calico struct {
	// Enable wireguard-based encryption (default: false)
	EnableWireguard bool `json:"wireguard"`

	// The host path for Calicos flex-volume-driver(default: /usr/libexec/k0s/kubelet-plugins/volume/exec/nodeagent~uds)
	// +kubebuilder:default=/usr/libexec/k0s/kubelet-plugins/volume/exec/nodeagent~uds
	FlexVolumeDriverPath string `json:"flexVolumeDriverPath"`

	// Host's IP Auto-detection method for Calico (see https://docs.projectcalico.org/reference/node/configuration#ip-autodetection-methods)
	// +optional
	IPAutodetectionMethod string `json:"ipAutodetectionMethod,omitempty"`

	// Host's IPv6 Auto-detection method for Calico
	// +optional
	IPv6AutodetectionMethod string `json:"ipV6AutodetectionMethod,omitempty"`

	// MTU for overlay network (default: 0)
	// +kubebuilder:default=0
	MTU uint32 `json:"mtu" yaml:"mtu"`

	// vxlan (default) or ipip
	// +kubebuilder:default=vxlan
	// +kubebuilder:validation:Enum=vxlan;ipip
	Mode string `json:"mode"`

	// Overlay Type (Always, Never or CrossSubnet)
	// +kubebuilder:validation:Enum=Always;Never;CrossSubnet
	Overlay string `json:"overlay"`

	// The UDP port for VXLAN (default: 4789)
	// +kubebuilder:default=4789
	VxlanPort IPPort `json:"vxlanPort"`

	// The virtual network ID for VXLAN (default: 4096)
	// +kubebuilder:default=4096
	VxlanVNI int `json:"vxlanVNI"`

	// Windows Nodes (default: false)
	// +optional
	WithWindowsNodes bool `json:"withWindowsNodes,omitempty"`
}

// DefaultCalico returns sane defaults for calico
func DefaultCalico() *Calico {
	return &Calico{
		Mode:                    "vxlan",
		VxlanPort:               4789,
		VxlanVNI:                4096,
		MTU:                     0,
		EnableWireguard:         false,
		FlexVolumeDriverPath:    "/usr/libexec/k0s/kubelet-plugins/volume/exec/nodeagent~uds",
		WithWindowsNodes:        false,
		Overlay:                 "Always",
		IPAutodetectionMethod:   "",
		IPv6AutodetectionMethod: "",
	}
}

// UnmarshalJSON sets in some sane defaults when unmarshaling the data from JSON
func (c *Calico) UnmarshalJSON(data []byte) error {
	c.Mode = "vxlan"
	c.VxlanPort = 4789
	c.VxlanVNI = 4096
	c.MTU = 1450
	c.EnableWireguard = false
	c.WithWindowsNodes = false
	c.FlexVolumeDriverPath = "/usr/libexec/kubernetes/kubelet-plugins/volume/exec/nodeagent~uds"
	c.Overlay = "Always"
	c.IPAutodetectionMethod = ""
	c.IPv6AutodetectionMethod = ""

	type calico Calico
	jc := (*calico)(c)
	return json.Unmarshal(data, jc)
}
