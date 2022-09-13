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
	// +optional
	EnableWireguard bool `json:"wireguard,omitempty"`

	// The host path for Calicos flex-volume-driver(default: /usr/libexec/k0s/kubelet-plugins/volume/exec/nodeagent~uds)
	// +optional
	// +kubebuilder:default=/usr/libexec/k0s/kubelet-plugins/volume/exec/nodeagent~uds
	FlexVolumeDriverPath string `json:"flexVolumeDriverPath,omitempty"`

	// Host's IP Auto-detection method for Calico (see https://docs.projectcalico.org/reference/node/configuration#ip-autodetection-methods)
	// +optional
	IPAutodetectionMethod string `json:"ipAutodetectionMethod,omitempty"`

	// Host's IPv6 Auto-detection method for Calico
	// +optional
	IPv6AutodetectionMethod string `json:"ipV6AutodetectionMethod,omitempty"`

	// MTU for overlay network (default: 0, which causes Calico to detect optimal MTU during bootstrap).
	// +optional
	// +kubebuilder:default=0
	// +kubebuilder:validation:Minimum=0
	MTU uint32 `json:"mtu,omitempty"`

	// vxlan (default) or ipip
	// +optional
	// +kubebuilder:default=vxlan
	// +kubebuilder:validation:Enum=vxlan;ipip
	Mode string `json:"mode,omitempty"`

	// Overlay Type (Always, Never or CrossSubnet)
	// +optional
	// +kubebuilder:default=Always
	// +kubebuilder:validation:Enum=Always;Never;CrossSubnet
	Overlay string `json:"overlay,omitempty"`

	// The UDP port for VXLAN (default: 4789)
	// +optional
	// +kubebuilder:default=4789
	VxlanPort IPPort `json:"vxlanPort,omitempty"`

	// The virtual network ID for VXLAN (default: 4096)
	// +optional
	// +kubebuilder:default=4096
	VxlanVNI int `json:"vxlanVNI,omitempty"`

	// Windows Nodes (default: false)
	// +optional
	WithWindowsNodes bool `json:"withWindowsNodes,omitempty"`
}

// DefaultCalico returns sane defaults for calico
func DefaultCalico() *Calico {
	c := new(Calico)
	SetDefaults_Calico(c)
	return c
}

func SetDefaults_Calico(c *Calico) {
	if c.FlexVolumeDriverPath == "" {
		c.FlexVolumeDriverPath = "/usr/libexec/k0s/kubelet-plugins/volume/exec/nodeagent~uds"
	}

	if c.Mode == "" {
		c.Mode = "vxlan"
	}

	if c.Overlay == "" {
		c.Overlay = "Always"
	}

	c.VxlanPort = c.VxlanPort.Or(4789)

	if c.VxlanVNI == 0 {
		c.VxlanVNI = 4096
	}
}

// UnmarshalJSON sets in some sane defaults when unmarshaling the data from JSON
func (c *Calico) UnmarshalJSON(data []byte) error {
	SetDefaults_Calico(c)
	type calico Calico
	jc := (*calico)(c)
	return json.Unmarshal(data, jc)
}
