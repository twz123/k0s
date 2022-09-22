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
	"github.com/k0sproject/k0s/pkg/constant"

	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/utils/pointer"
)

// NodeLocalLoadBalancer defines the configuration options related to k0s's
// node-local load balancing feature.
// NOTE: This feature is experimental, and currently unsupported on ARMv7!
type NodeLocalLoadBalancer struct {
	// enabled indicates if node-local load balancing should be used to access
	// Kubernetes API servers from worker nodes.
	// Default: true
	// +optional
	Enabled *bool `json:"enabled,omitempty"`

	// type indicates the type of the node-local load balancer to deploy.
	// Currently, the only supported type is "EnvoyProxy".
	// +optional
	Type NllbType `json:"type,omitempty"`

	// envoyProxy contains configuration options related to the "EnvoyProxy" type
	// of load balancing.
	EnvoyProxy *EnvoyProxy `json:"envoyProxy,omitempty"`
}

// ConcurrencyPolicy describes how the job will be handled.
// Only one of the following concurrent policies may be specified.
// If none of the following policies is specified, the default one
// is AllowConcurrent.
// +kubebuilder:validation:Enum=EnvoyProxy
type NllbType string

const (
	// NllbTypeEnvoyProxy selects Envoy as the backing load balancer.
	NllbTypeEnvoyProxy NllbType = "EnvoyProxy"
)

// DefaultNodeLocalLoadBalancer returns the default node-local load balancing configuration.
func DefaultNodeLocalLoadBalancer(clusterImages *ClusterImages) *NodeLocalLoadBalancer {
	return &NodeLocalLoadBalancer{
		Enabled:    pointer.Bool(false),
		Type:       NllbTypeEnvoyProxy,
		EnvoyProxy: DefaultEnvoyProxy(clusterImages),
	}
}

func (n *NodeLocalLoadBalancer) Validate(path *field.Path) (errs field.ErrorList) {
	if n == nil {
		return
	}

	switch n.Type {
	case NllbTypeEnvoyProxy:
		errs = append(errs, n.EnvoyProxy.Validate(path.Child("envoyProxy"))...)
		// nothing to validate
	default:
		errs = append(errs, field.NotSupported(path.Child("type"), n.Type, []string{string(NllbTypeEnvoyProxy)}))
	}

	return
}

func (n *NodeLocalLoadBalancer) IsEnabled() bool {
	return n != nil && (n.Enabled == nil || *n.Enabled)
}

// EnvoyProxy describes configuration options required for using Envoy as the
// backing implementation for node-local load balancing.
type EnvoyProxy struct {
	// image specifies the OCI image that's being used to run Envoy.
	// +optional
	Image *ImageSpec `json:"image"`

	// apiServerBindPort is the port number to bind Envoy to on a worker's
	// loopback interface. This must be a valid port number, 0 < x < 65536.
	// Default: 7443
	// +optional
	// +kubebuilder:default=7443
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	APIServerBindPort int32 `json:"apiServerBindPort,omitempty"`

	// konnectivityAgentBindPort is the port number to bind Envoy to on a
	// worker's loopback interface. This must be a valid port number, 0 < x <
	// 65536.
	// Default: 7132
	// +optional
	// +kubebuilder:default=7132
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	KonnectivityAgentBindPort *int32 `json:"konnectivityAgentBindPort,omitempty"`
}

// DefaultNodeLocalLoadBalancer returns the default node-local load balancing configuration.
func DefaultEnvoyProxy(clusterImages *ClusterImages) *EnvoyProxy {
	return &EnvoyProxy{
		Image:                     DefaultEnvoyProxyImage(clusterImages),
		APIServerBindPort:         7443,
		KonnectivityAgentBindPort: pointer.Int32(7132),
	}
}

func (p *EnvoyProxy) Validate(path *field.Path) (errs field.ErrorList) {
	if p == nil {
		return
	}

	errs = append(errs, p.Image.Validate(path.Child("image"))...)

	if details := validation.IsValidPortNum(int(p.APIServerBindPort)); len(details) > 0 {
		path := path.Child("apiServerBindPort")
		for _, detail := range details {
			errs = append(errs, field.Invalid(path, p.APIServerBindPort, detail))
		}
	}

	if p.KonnectivityAgentBindPort != nil {
		if details := validation.IsValidPortNum(int(*p.KonnectivityAgentBindPort)); len(details) > 0 {
			path := path.Child("konnectivityAgentBindPort")
			for _, detail := range details {
				errs = append(errs, field.Invalid(path, p.KonnectivityAgentBindPort, detail))
			}
		}
	}

	return
}

// DefaultEnvoyProxyImage returns the default image spec to use for Envoy.
func DefaultEnvoyProxyImage(clusterImages *ClusterImages) *ImageSpec {
	image := &ImageSpec{
		Image:   constant.EnvoyProxyImage,
		Version: constant.EnvoyProxyImageVersion,
	}
	clusterImages.OverrideImageRepository(image)
	return image
}
