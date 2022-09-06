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
	// +kubebuilder:validation:Enum=EnvoyProxy
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

func (n *NodeLocalLoadBalancer) IsEnabled() bool {
	return n != nil && (n.Enabled == nil || *n.Enabled)
}

func (n *NodeLocalLoadBalancer) DefaultedCopy(clusterImages *ClusterImages) *NodeLocalLoadBalancer {
	if !n.IsEnabled() {
		return nil
	}

	copy := new(NodeLocalLoadBalancer)
	copy.Type = n.Type
	if copy.Type == NllbTypeEnvoyProxy {
		copy.EnvoyProxy = n.EnvoyProxy.DeepCopy()
		if copy.EnvoyProxy == nil {
			copy.EnvoyProxy = DefaultEnvoyProxy(clusterImages)
		} else if copy.EnvoyProxy.Image == nil {
			copy.EnvoyProxy.Image = DefaultEnvoyProxyImage(clusterImages)
		}
	}

	return copy
}

// EnvoyProxy describes configuration options required for using Envoy as the
// backing implementation for node-local load balancing.
type EnvoyProxy struct {
	// image specifies the OCI image that's being used to run Envoy.
	Image *ImageSpec `json:"image"`

	// port is the port number to bind Envoy to on a worker's loopback
	// interface. This must be a valid port number, 0 < x < 65536.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65536
	Port int32 `json:"port,omitempty"`
}

// DefaultNodeLocalLoadBalancer returns the default node-local load balancing configuration.
func DefaultEnvoyProxy(clusterImages *ClusterImages) *EnvoyProxy {
	return &EnvoyProxy{
		Image: DefaultEnvoyProxyImage(clusterImages),
		Port:  7443,
	}
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
