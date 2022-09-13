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

// KonnectivitySpec defines the requested state for Konnectivity
type KonnectivitySpec struct {
	// agent port to listen on (default 8132)
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	// +kubebuilder:default=8132
	// +optional
	AgentPort int32 `json:"agentPort,omitempty"`

	// admin port to listen on (default 8133)
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	// +kubebuilder:default=8133
	// +optional
	AdminPort int32 `json:"adminPort,omitempty"`
}

// DefaultKonnectivitySpec builds default KonnectivitySpec
func DefaultKonnectivitySpec() *KonnectivitySpec {
	return &KonnectivitySpec{
		AgentPort: 8132,
		AdminPort: 8133,
	}
}

// Validate stub for Validateable interface
func (k *KonnectivitySpec) Validate() []error {
	return nil
}
