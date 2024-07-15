/*
Copyright 2020 k0s authors

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

	"github.com/k0sproject/k0s/internal/defaults"
)

// Calico defines the calico related config options
type Calico struct {
	// Enable wireguard-based encryption (default: false)
	EnableWireguard bool `json:"wireguard,omitempty"`

	// Environment variables to configure Calico node (see https://docs.projectcalico.org/reference/node/configuration)
	EnvVars map[string]string `json:"envVars,omitempty"`

	// The host path for Calicos flex-volume-driver(default: /usr/libexec/k0s/kubelet-plugins/volume/exec/nodeagent~uds)
	// +kubebuilder:default="/usr/libexec/k0s/kubelet-plugins/volume/exec/nodeagent~uds"
	FlexVolumeDriverPath string `json:"flexVolumeDriverPath,omitempty"`

	// Host's IP Auto-detection method for Calico (see https://docs.projectcalico.org/reference/node/configuration#ip-autodetection-methods)
	IPAutodetectionMethod string `json:"ipAutodetectionMethod,omitempty"`

	// Host's IPv6 Auto-detection method for Calico
	IPv6AutodetectionMethod string `json:"ipV6AutodetectionMethod,omitempty"`

	// MTU for overlay network (default: 1450)
	// +kubebuilder:default=1450
	MTU int `json:"mtu,omitempty"`

	// vxlan (default) or ipip
	// +kubebuilder:default=vxlan
	Mode string `json:"mode,omitempty"`

	// Overlay Type (Always, Never or CrossSubnet)
	// +kubebuilder:default=Always
	Overlay string `json:"overlay,omitempty"`

	// The UDP port for VXLAN (default: 4789)
	// +kubebuilder:default=4789
	VxlanPort int `json:"vxlanPort,omitempty"`

	// The virtual network ID for VXLAN (default: 4096)
	// +kubebuilder:default=4096
	VxlanVNI int `json:"vxlanVNI,omitempty"`
}

// DefaultCalico returns sane defaults for calico
func DefaultCalico() *Calico {
	return defaults.New(SetDefaults_Calico)
}

// UnmarshalJSON sets in some sane defaults when unmarshaling the data from JSON
func (c *Calico) UnmarshalJSON(data []byte) error {
	SetDefaults_Calico(c)

	type calico Calico
	jc := (*calico)(c)
	return json.Unmarshal(data, jc)
}

func SetDefaults_Calico(c *Calico) {
	defaults.IfZero(&c.Mode).To("vxlan")
	defaults.IfZero(&c.VxlanPort).To(4789)
	defaults.IfZero(&c.VxlanVNI).To(4096)
	defaults.IfZero(&c.MTU).To(1450)
	defaults.IfZero(&c.FlexVolumeDriverPath).To("/usr/libexec/kubernetes/kubelet-plugins/volume/exec/nodeagent~uds")
	defaults.IfZero(&c.Overlay).To("Always")
}
