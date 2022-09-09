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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation/field"

	"github.com/k0sproject/k0s/internal/pkg/strictyaml"
)

const (
	ClusterConfigKind       = "ClusterConfig"
	ClusterConfigAPIVersion = "k0s.k0sproject.io/v1beta1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// ClusterSpec defines the desired state of ClusterConfig
type ClusterSpec struct {
	API               *APISpec               `json:"api"`
	ControllerManager *ControllerManagerSpec `json:"controllerManager,omitempty"`
	Scheduler         *SchedulerSpec         `json:"scheduler,omitempty"`
	Storage           *StorageSpec           `json:"storage"`
	Network           *Network               `json:"network"`
	WorkerProfiles    WorkerProfiles         `json:"workerProfiles,omitempty"`
	Telemetry         *ClusterTelemetry      `json:"telemetry"`
	Install           *InstallSpec           `json:"installConfig,omitempty"`
	Images            *ClusterImages         `json:"images"`
	Extensions        *ClusterExtensions     `json:"extensions,omitempty"`
	Konnectivity      *KonnectivitySpec      `json:"konnectivity,omitempty"`
}

// ClusterConfigStatus defines the observed state of ClusterConfig
type ClusterConfigStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:validation:Optional
// +genclient
// +genclient:onlyVerbs=create,delete,list,get,watch,update
// +groupName=k0s.k0sproject.io

// ClusterConfig is the Schema for the clusterconfigs API
type ClusterConfig struct {
	metav1.ObjectMeta `json:"metadata,omitempty"`
	metav1.TypeMeta   `json:",omitempty,inline"`

	Spec   *ClusterSpec        `json:"spec,omitempty"`
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
	if reflect.DeepEqual(copy.Spec.Images, DefaultClusterImages()) {
		copy.Spec.Images = nil
	}
	if reflect.DeepEqual(copy.Spec.Network, DefaultNetwork(copy.Spec.Images)) {
		copy.Spec.Network = nil
	}
	if reflect.DeepEqual(copy.Spec.Telemetry, DefaultClusterTelemetry()) {
		copy.Spec.Telemetry = nil
	}
	if reflect.DeepEqual(copy.Spec.Konnectivity, DefaultKonnectivitySpec()) {
		copy.Spec.Konnectivity = nil
	}
	return copy
}

// InstallSpec defines the required fields for the `k0s install` command
type InstallSpec struct {
	SystemUsers *SystemUser `json:"users,omitempty"`
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
	if config.Spec == nil {
		config.Spec = DefaultClusterSpec(defaultStorage...)
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
	clusterSpec := DefaultClusterSpec(defaultStorage...)
	return &ClusterConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "k0s"},
		TypeMeta: metav1.TypeMeta{
			APIVersion: "k0s.k0sproject.io/v1beta1",
			Kind:       "ClusterConfig",
		},
		Spec: clusterSpec,
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
	if c.Spec != nil && c.Spec.Storage != nil {
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
func DefaultClusterSpec(defaultStorage ...*StorageSpec) *ClusterSpec {
	var storage *StorageSpec
	if defaultStorage == nil || defaultStorage[0] == nil {
		storage = DefaultStorageSpec()
	} else {
		storage = defaultStorage[0]
	}

	defaultClusterImages := DefaultClusterImages()
	return &ClusterSpec{
		Extensions:        DefaultExtensions(),
		Storage:           storage,
		Network:           DefaultNetwork(defaultClusterImages),
		API:               DefaultAPISpec(),
		ControllerManager: DefaultControllerManagerSpec(),
		Scheduler:         DefaultSchedulerSpec(),
		Install:           DefaultInstallSpec(),
		Images:            defaultClusterImages,
		Telemetry:         DefaultClusterTelemetry(),
		Konnectivity:      DefaultKonnectivitySpec(),
	}
}

func (s *ClusterSpec) Validate() (errs []error) {
	if s == nil {
		return
	}

	for name, v := range map[string]interface{ Validate() []error }{
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
		for _, err := range v.Validate() {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
		}
	}

	for _, vErr := range s.ValidateNodeLocalLoadBalancer(field.NewPath("spec")) {
		errs = append(errs, vErr)
	}

	return
}

func (s *ClusterSpec) ValidateNodeLocalLoadBalancer(path *field.Path) (errs field.ErrorList) {
	if s.Network == nil || !s.Network.NodeLocalLoadBalancer.IsEnabled() {
		return
	}

	if s.API == nil {
		return
	}

	path = path.Child("network", "nodeLocalLoadBalancer")
	if s.API.TunneledNetworkingMode {
		detail := "node-local load balancing cannot be used in tunneled networking mode"
		errs = append(errs, field.Forbidden(path, detail))
	}

	if s.API.ExternalAddress != "" {
		detail := "node-local load balancing cannot be used in conjunction with an external Kubernetes API server address"
		errs = append(errs, field.Forbidden(path, detail))
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

// GetNodeConfig returns a [ClusterConfig] object stripped of
// cluster-wide settings. It will only contain node-specific parts:
//   - Spec.API
//   - Spec.Storage
//   - Spec.Network.ServiceCIDR
//   - Spec.Install
//
// This is the counterpart to [ClusterConfig.GetClusterWideConfig].
func (c *ClusterConfig) GetNodeConfig() *ClusterConfig {
	if c == nil {
		return nil
	}

	config := ClusterConfig{
		ObjectMeta: c.ObjectMeta,
		TypeMeta:   c.TypeMeta,
	}

	if c.Spec != nil {
		config.Spec = &ClusterSpec{
			API:     c.Spec.API.DeepCopy(),
			Storage: c.Spec.Storage.DeepCopy(),
			Install: c.Spec.Install.DeepCopy(),
		}

		if c.Spec.Network != nil {
			config.Spec.Network = &Network{
				ServiceCIDR: c.Spec.Network.ServiceCIDR,
			}
		}
	}

	return &config
}

// HACK: the current [ClusterConfig] struct holds both bootstrapping config and
// cluster-wide config. This hack strips away the node-specific bootstrapping
// config so that we write a "clean" config to the cluster. This function
// accepts a standard ClusterConfig and returns the same config minus the node
// specific parts:
//   - Spec.API
//   - Spec.Storage
//   - Spec.Network.ServiceCIDR
//   - Spec.Install
//
// This is the counterpart to [ClusterConfig.GetBootstrappingConfig].
func (c *ClusterConfig) GetClusterWideConfig() *ClusterConfig {
	copy := c.DeepCopy()
	if copy.Spec != nil {
		copy.Spec.API = nil
		copy.Spec.Storage = nil
		if copy.Spec.Network != nil {
			copy.Spec.Network.ServiceCIDR = ""
		}
		copy.Spec.Install = nil
	}

	return copy
}

// CRValidator is used to make sure a config CR is created with correct values
func (c *ClusterConfig) CRValidator() *ClusterConfig {
	copy := c.DeepCopy()
	copy.ObjectMeta.Name = "k0s"
	copy.ObjectMeta.Namespace = "kube-system"

	return copy
}
