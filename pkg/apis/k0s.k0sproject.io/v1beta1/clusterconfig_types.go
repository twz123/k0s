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
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"

	"github.com/k0sproject/k0s/internal/pkg/strictyaml"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

const (
	ClusterConfigKind       = "ClusterConfig"
	ClusterConfigAPIVersion = "k0s.k0sproject.io/v1beta1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// ClusterSpec defines the desired state of ClusterConfig
type ClusterSpec struct {
	// +optional
	API *APISpec `json:"api,omitempty"`

	// +optional
	ControllerManager *ControllerManagerSpec `json:"controllerManager,omitempty"`

	// +optional
	Scheduler *SchedulerSpec `json:"scheduler,omitempty"`

	// +optional
	Storage *StorageSpec `json:"storage,omitempty"`

	// +optional
	Network *Network `json:"network,omitempty"`

	// +optional
	WorkerProfiles WorkerProfiles `json:"workerProfiles,omitempty"`

	// +optional
	Telemetry *ClusterTelemetry `json:"telemetry,omitempty"`

	// +optional
	Install *InstallSpec `json:"installConfig,omitempty"`

	// +optional
	Images *ClusterImages `json:"images,omitempty"`

	// +optional
	Extensions *ClusterExtensions `json:"extensions,omitempty"`

	// +optional
	Konnectivity *KonnectivitySpec `json:"konnectivity,omitempty"`
}

// ClusterConfigStatus defines the observed state of ClusterConfig
type ClusterConfigStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
}

// ClusterConfig is the Schema for the clusterconfigs API
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:validation:Optional
// +genclient
// +genclient:onlyVerbs=create,delete,list,get,watch,update
type ClusterConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec ClusterSpec `json:"spec"`

	// +optional
	Status ClusterConfigStatus `json:"status,omitempty"`
}

// StripDefaults returns a copy of the config where the default values a nilled out
func (c *ClusterConfig) StripDefaults() *ClusterConfig {
	copy := c.DeepCopy()
	if reflect.DeepEqual(copy.Spec.API, DefaultAPISpec()) {
		copy.Spec.API = nil
	}
	if reflect.DeepEqual(copy.Spec.ControllerManager, DefaultControllerManagerSpec()) {
		copy.Spec.ControllerManager = nil
	}
	if reflect.DeepEqual(copy.Spec.Scheduler, DefaultSchedulerSpec()) {
		copy.Spec.Scheduler = nil
	}
	if reflect.DeepEqual(c.Spec.Storage, DefaultStorageSpec()) {
		c.Spec.ControllerManager = nil
	}
	if reflect.DeepEqual(copy.Spec.Network, DefaultNetwork()) {
		copy.Spec.Network = nil
	}
	if reflect.DeepEqual(copy.Spec.Telemetry, DefaultClusterTelemetry()) {
		copy.Spec.Telemetry = nil
	}
	if reflect.DeepEqual(copy.Spec.Images, DefaultClusterImages()) {
		copy.Spec.Images = nil
	}
	if reflect.DeepEqual(copy.Spec.Konnectivity, DefaultKonnectivitySpec()) {
		copy.Spec.Konnectivity = nil
	}
	return copy
}

// InstallSpec defines the required fields for the `k0s install` command
type InstallSpec struct {
	Users SystemUsers `json:"users,omitempty"`
}

// DefaultInstallSpec ...
func DefaultInstallSpec() *InstallSpec {
	return &InstallSpec{
		Users: DefaultSystemUsers(),
	}
}

func (*InstallSpec) Validate() []error { return nil }

// ControllerManagerSpec defines the fields for the ControllerManager
type ControllerManagerSpec struct {
	// Map of key-values (strings) for any extra arguments you want to pass down to the Kubernetes controller manager process
	ExtraArgs map[string]string `json:"extraArgs,omitempty"`
}

func DefaultControllerManagerSpec() *ControllerManagerSpec {
	return &ControllerManagerSpec{
		ExtraArgs: make(map[string]string),
	}
}

// SchedulerSpec defines the fields for the Scheduler
type SchedulerSpec struct {
	// Map of key-values (strings) for any extra arguments you want to pass down to Kubernetes scheduler process
	ExtraArgs map[string]string `json:"extraArgs,omitempty"`
}

func DefaultSchedulerSpec() *SchedulerSpec {
	return &SchedulerSpec{
		ExtraArgs: make(map[string]string),
	}
}

func (*SchedulerSpec) Validate() []error { return nil }

// +kubebuilder:object:root=true
// +genclient
// +genclient:onlyVerbs=create
// ClusterConfigList contains a list of ClusterConfig
type ClusterConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ClusterConfig{}, &ClusterConfigList{})
}

// IsZero needed to omit empty object from yaml output
func (c *ControllerManagerSpec) IsZero() bool {
	return len(c.ExtraArgs) == 0
}

// IsZero needed to omit empty object from yaml output
func (s *SchedulerSpec) IsZero() bool {
	return len(s.ExtraArgs) == 0
}

func ConfigFromString(yml string, defaultStorage ...*StorageSpec) (*ClusterConfig, error) {
	config := DefaultClusterConfig(defaultStorage...)
	err := strictyaml.YamlUnmarshalStrictIgnoringFields([]byte(yml), config, "interval", "podSecurityPolicy")
	if err != nil {
		return config, err
	}
	return config, nil
}

// ConfigFromReader reads the configuration from any reader (can be stdin, file reader, etc)
func ConfigFromReader(r io.Reader, defaultStorage ...*StorageSpec) (*ClusterConfig, error) {
	input, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	return ConfigFromString(string(input), defaultStorage...)
}

// DefaultClusterConfig sets the default ClusterConfig values, when none are given
func DefaultClusterConfig(defaultStorage ...*StorageSpec) *ClusterConfig {
	return &ClusterConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "k0s"},
		TypeMeta: metav1.TypeMeta{
			APIVersion: "k0s.k0sproject.io/v1beta1",
			Kind:       "ClusterConfig",
		},
		Spec: DefaultClusterSpec(defaultStorage...),
	}
}

// UnmarshalJSON sets in some sane defaults when unmarshaling the data from json
func (c *ClusterConfig) UnmarshalJSON(data []byte) error {
	if c.Kind == "" {
		c.Kind = "ClusterConfig"
	}

	// If there's already a storage configured, do not override it with default
	// etcd config BEFORE unmarshaling
	var storage *StorageSpec
	if c.Spec.Storage != nil {
		storage = c.Spec.Storage
	}
	c.Spec = DefaultClusterSpec(storage)

	type config ClusterConfig
	jc := (*config)(c)

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()

	return decoder.Decode(jc)
}

// DefaultClusterSpec default settings
func DefaultClusterSpec(defaultStorage ...*StorageSpec) ClusterSpec {
	var storage *StorageSpec
	if defaultStorage == nil || defaultStorage[0] == nil {
		storage = DefaultStorageSpec()
	} else {
		storage = defaultStorage[0]
	}

	return ClusterSpec{
		Extensions:        DefaultExtensions(),
		Storage:           storage,
		Network:           DefaultNetwork(),
		API:               DefaultAPISpec(),
		ControllerManager: DefaultControllerManagerSpec(),
		Scheduler:         DefaultSchedulerSpec(),
		Install:           DefaultInstallSpec(),
		Images:            DefaultClusterImages(),
		Telemetry:         DefaultClusterTelemetry(),
		Konnectivity:      DefaultKonnectivitySpec(),
	}
}

func (s *ClusterSpec) Validate() (errs []error) {
	if s == nil {
		return
	}

	path := field.NewPath("spec")
	for name, v := range map[string]interface{}{
		"api":               s.API,
		"controllerManager": s.ControllerManager,
		"scheduler":         s.Scheduler,
		"storage":           s.Storage,
		"network":           s.Network,
		"workerProfiles":    s.WorkerProfiles,
		"telemetry":         s.Telemetry,
		"install":           s.Install,
		"extensions":        s.Extensions,
		"konnectivity":      s.Konnectivity,
	} {
		if v == nil || reflect.ValueOf(v).IsNil() {
			continue
		}
		switch x := v.(type) {
		case interface{ Validate() []error }:
			for _, err := range x.Validate() {
				errs = append(errs, fmt.Errorf("%s: %w", name, err))
			}
		case interface {
			Validate(*field.Path) field.ErrorList
		}:
			for _, err := range x.Validate(path.Child(name)) {
				errs = append(errs, fmt.Errorf("%s: %w", name, err))
			}
		}
	}

	return
}

func (c *ControllerManagerSpec) Validate() []error {
	return nil
}

// Validate validates cluster config
func (c *ClusterConfig) Validate() []error {
	return c.Spec.Validate()
}

// GetBootstrappingConfig returns a ClusterConfig object stripped of Cluster-Wide Settings
func (c *ClusterConfig) GetBootstrappingConfig(storageSpec *StorageSpec) *ClusterConfig {
	var etcdConfig *EtcdConfig
	if storageSpec.Type == EtcdStorageType {
		etcdConfig = &EtcdConfig{
			ExternalCluster: storageSpec.Etcd.ExternalCluster,
			PeerAddress:     storageSpec.Etcd.PeerAddress,
		}
		c.Spec.Storage.Etcd = etcdConfig
	}
	return &ClusterConfig{
		ObjectMeta: c.ObjectMeta,
		TypeMeta:   c.TypeMeta,
		Spec: ClusterSpec{
			API:     c.Spec.API,
			Storage: storageSpec,
			Network: &Network{
				ServiceCIDR: c.Spec.Network.ServiceCIDR,
				DualStack:   c.Spec.Network.DualStack,
			},
			Install: c.Spec.Install,
		},
		Status: c.Status,
	}
}

// HACK: the current ClusterConfig struct holds both bootstrapping config & cluster-wide config
// this hack strips away the node-specific bootstrapping config so that we write a "clean" config to the CR
// This function accepts a standard ClusterConfig and returns the same config minus the node specific info:
// - APISpec
// - StorageSpec
// - Network.ServiceCIDR
// - Install
func (c *ClusterConfig) GetClusterWideConfig() *ClusterConfig {
	return &ClusterConfig{
		ObjectMeta: c.ObjectMeta,
		TypeMeta:   c.TypeMeta,
		Spec: ClusterSpec{
			ControllerManager: c.Spec.ControllerManager,
			Scheduler:         c.Spec.Scheduler,
			Network: &Network{
				Calico:     c.Spec.Network.Calico,
				KubeProxy:  c.Spec.Network.KubeProxy,
				KubeRouter: c.Spec.Network.KubeRouter,
				PodCIDR:    c.Spec.Network.PodCIDR,
				Provider:   c.Spec.Network.Provider,
			},
			WorkerProfiles: c.Spec.WorkerProfiles,
			Telemetry:      c.Spec.Telemetry,
			Images:         c.Spec.Images,
			Extensions:     c.Spec.Extensions,
			Konnectivity:   c.Spec.Konnectivity,
		},
		Status: c.Status,
	}
}

// CRValidator is used to make sure a config CR is created with correct values
func (c *ClusterConfig) CRValidator() *ClusterConfig {
	copy := c.DeepCopy()
	copy.ObjectMeta.Name = "k0s"
	copy.ObjectMeta.Namespace = "kube-system"

	return copy
}
