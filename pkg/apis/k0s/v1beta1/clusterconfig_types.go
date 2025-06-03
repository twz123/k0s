/*
Copyright 2021 k0s authors

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
	"reflect"
	"slices"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/k0sproject/k0s/internal/pkg/strictyaml"
	"github.com/k0sproject/k0s/pkg/constant"
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
	API               *APISpec               `json:"api,omitempty"`
	ControllerManager *ControllerManagerSpec `json:"controllerManager,omitempty"`
	Scheduler         *SchedulerSpec         `json:"scheduler,omitempty"`
	Storage           *StorageSpec           `json:"storage,omitempty"`
	Network           *Network               `json:"network,omitempty"`
	WorkerProfiles    WorkerProfiles         `json:"workerProfiles,omitempty"`
	Telemetry         *ClusterTelemetry      `json:"telemetry,omitempty"`
	Install           *InstallSpec           `json:"installConfig,omitempty"`
	Images            *ClusterImages         `json:"images,omitempty"`
	Extensions        *ClusterExtensions     `json:"extensions,omitempty"`
	Konnectivity      *KonnectivitySpec      `json:"konnectivity,omitempty"`
	FeatureGates      FeatureGates           `json:"featureGates,omitempty"`
}

// ClusterConfigStatus defines the observed state of ClusterConfig
type ClusterConfigStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
}

// ClusterConfig is the Schema for the clusterconfigs API
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +genclient
// +genclient:onlyVerbs=create,delete,list,get,watch,update
type ClusterConfig struct {
	metav1.ObjectMeta `json:"metadata,omitempty"`
	metav1.TypeMeta   `json:",omitempty,inline"`

	Spec   *ClusterSpec         `json:"spec,omitempty"`
	Status *ClusterConfigStatus `json:"status,omitempty"`
}

// Zeroes out any field in actualValue whose value equals the corresponding
// field in defaultValue, but only if that field's JSON tag contains
// "omitempty". Both actualValue and defaultValue must be wrapping the same
// struct type, actualValue must be addressable so its fields can be set, and
// defaultValue is never modified. Unexported fields and fields without
// "omitempty" (including json:\"-\") are left untouched.
//
// This logic will be applied recursively, i.e. stripDefaults will be called on
// nested structs (or pointers to them). All other types will be handled at the
// top level only.
func stripDefaults(actualValue, defaultValue reflect.Value) {
	typ := actualValue.Type()
	for i := range typ.NumField() {
		field := typ.Field(i)

		switch field.Type.Kind() {
		case reflect.Pointer:
			// Skip fields to be ignored.
			if !field.IsExported() || !canStrip(field) {
				continue
			}

			actualValue, defaultValue := actualValue.Field(i), defaultValue.Field(i)

			// Skip over nil pointers.
			if actualValue.IsNil() || defaultValue.IsNil() {
				continue
			}

			// Dereference pointers.
			actualElem, defaultElem := actualValue.Elem(), defaultValue.Elem()

			if reflect.DeepEqual(actualElem.Interface(), defaultElem.Interface()) {
				// Underlying values are equal, nil out pointer.
				actualValue.SetZero()
			} else if actualElem.Kind() == reflect.Struct {
				// Underlying values are different, recurse into the pointed struct.
				stripDefaults(actualElem, defaultElem)
				// Nil out pointer if only the zero value remains.
				if actualElem.IsZero() {
					actualValue.SetZero()
				}
			}

		case reflect.Struct:
			// Recurse into structs. The omitempty tag is meaningless for them.
			if field.IsExported() {
				stripDefaults(actualValue.Field(i), defaultValue.Field(i))
			}

		default:
			// Skip fields to be ignored.
			if !field.IsExported() || !canStrip(field) {
				continue
			}

			actualValue, defaultValue := actualValue.Field(i), defaultValue.Field(i)
			if reflect.DeepEqual(actualValue.Interface(), defaultValue.Interface()) {
				actualValue.SetZero()
			}
		}
	}
}

// Indicates whether a struct field is eligible for stripping defaults: it
// returns true if the JSON tag includes "omitempty" and the field is not
// explicitly ignored. Fields tagged `json:"-"` (or `json:"-,omitempty"`) are
// never stripped.
func canStrip(f reflect.StructField) bool {
	if name, tags, hasTags := strings.Cut(f.Tag.Get("json"), ","); hasTags && name != "-" {
		tags := strings.Split(tags, ",")
		return slices.Contains(tags, "omitempty")
	}

	return false
}

// InstallSpec defines the required fields for the `k0s install` command
type InstallSpec struct {
	SystemUsers *SystemUser `json:"users,omitempty"`
}

var _ Validateable = (*InstallSpec)(nil)

func (*InstallSpec) Validate() []error { return nil }

// ControllerManagerSpec defines the fields for the ControllerManager
type ControllerManagerSpec struct {
	// Map of key-values (strings) for any extra arguments you want to pass down to the Kubernetes controller manager process
	ExtraArgs map[string]string `json:"extraArgs,omitempty"`
}

var _ Validateable = (*ControllerManagerSpec)(nil)

func DefaultControllerManagerSpec() *ControllerManagerSpec {
	return &ControllerManagerSpec{
		ExtraArgs: make(map[string]string),
	}
}

func (c *ControllerManagerSpec) Validate() []error { return nil }

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

var _ Validateable = (*SchedulerSpec)(nil)

func (*SchedulerSpec) Validate() []error { return nil }

// +kubebuilder:object:root=true
// ClusterConfigList contains a list of ClusterConfig
type ClusterConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterConfig `json:"items"`
}

var _ Validateable = (*ControllerManagerSpec)(nil)

// IsZero needed to omit empty object from yaml output
func (c *ControllerManagerSpec) IsZero() bool {
	return len(c.ExtraArgs) == 0
}

// IsZero needed to omit empty object from yaml output
func (s *SchedulerSpec) IsZero() bool {
	return len(s.ExtraArgs) == 0
}

func ConfigFromBytes(bytes []byte) (*ClusterConfig, error) {
	return DefaultClusterConfig().MergedWithYAML(bytes)
}

func (c *ClusterConfig) MergedWithYAML(bytes []byte) (*ClusterConfig, error) {
	merged := c.DeepCopy()
	err := strictyaml.YamlUnmarshalStrictIgnoringFields(bytes, merged, "interval", "podSecurityPolicy")
	if err != nil {
		return nil, err
	}
	if merged.Spec == nil {
		merged.Spec = c.Spec
	}
	return merged, nil
}

// DefaultClusterConfig sets the default ClusterConfig values, when none are given
func DefaultClusterConfig() *ClusterConfig {
	return &ClusterConfig{
		TypeMeta: metav1.TypeMeta{
			APIVersion: GroupVersion.String(),
			Kind:       "ClusterConfig",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      constant.ClusterConfigObjectName,
			Namespace: constant.ClusterConfigNamespace,
		},
		Spec: DefaultClusterSpec(),
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
	c.Spec = DefaultClusterSpec()
	if storage != nil {
		c.Spec.Storage = storage
	}

	type config ClusterConfig
	jc := (*config)(c)

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()

	err := decoder.Decode(jc)
	if err != nil {
		return err
	}

	if jc.Spec == nil {
		jc.Spec = DefaultClusterSpec()
		if storage != nil {
			jc.Spec.Storage = storage
		}
		return nil
	}
	if jc.Spec.Storage == nil {
		jc.Spec.Storage = DefaultStorageSpec()
	}
	if jc.Spec.Extensions == nil {
		jc.Spec.Extensions = DefaultExtensions()
	}
	if jc.Spec.Network == nil {
		jc.Spec.Network = DefaultNetwork()
	}
	if jc.Spec.API == nil {
		jc.Spec.API = DefaultAPISpec()
	}
	if jc.Spec.ControllerManager == nil {
		jc.Spec.ControllerManager = DefaultControllerManagerSpec()
	}
	if jc.Spec.Scheduler == nil {
		jc.Spec.Scheduler = DefaultSchedulerSpec()
	}
	if jc.Spec.Install == nil {
		jc.Spec.Install = DefaultInstallSpec()
	}
	if jc.Spec.Images == nil {
		jc.Spec.Images = DefaultClusterImages()
	}
	if jc.Spec.Telemetry == nil {
		jc.Spec.Telemetry = DefaultClusterTelemetry()
	}
	if jc.Spec.Konnectivity == nil {
		jc.Spec.Konnectivity = DefaultKonnectivitySpec()
	}

	jc.Spec.overrideImageRepositories()

	return nil
}

// DefaultClusterSpec default settings
func DefaultClusterSpec() *ClusterSpec {
	spec := &ClusterSpec{
		Extensions:        DefaultExtensions(),
		Storage:           DefaultStorageSpec(),
		Network:           DefaultNetwork(),
		API:               DefaultAPISpec(),
		ControllerManager: DefaultControllerManagerSpec(),
		Scheduler:         DefaultSchedulerSpec(),
		Install:           DefaultInstallSpec(),
		Images:            DefaultClusterImages(),
		Telemetry:         DefaultClusterTelemetry(),
		Konnectivity:      DefaultKonnectivitySpec(),
	}

	spec.overrideImageRepositories()

	return spec
}

// Validateable interface to ensure that all config components implement Validate function
// +k8s:deepcopy-gen=false
type Validateable interface {
	Validate() []error
}

func (s *ClusterSpec) Validate() (errs []error) {
	if s == nil {
		return
	}

	for name, field := range map[string]Validateable{
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
		for _, err := range field.Validate() {
			errs = append(errs, fmt.Errorf("%s: %w", name, err))
		}
	}

	for _, err := range s.Images.Validate(field.NewPath("images")) {
		errs = append(errs, err)
	}

	for _, err := range s.ValidateNodeLocalLoadBalancing() {
		errs = append(errs, err)
	}

	if s.Network != nil && s.Network.ControlPlaneLoadBalancing != nil {
		for _, err := range s.Network.ControlPlaneLoadBalancing.Validate() {
			errs = append(errs, fmt.Errorf("controlPlaneLoadBalancing: %w", err))
		}
	}

	return
}

func (s *ClusterSpec) ValidateNodeLocalLoadBalancing() (errs field.ErrorList) {
	if s.Network == nil || !s.Network.NodeLocalLoadBalancing.IsEnabled() {
		return
	}

	if s.API == nil {
		return
	}

	path := field.NewPath("network", "nodeLocalLoadBalancing", "enabled")

	if s.API.ExternalAddress != "" {
		detail := "node-local load balancing cannot be used in conjunction with an external Kubernetes API server address"
		errs = append(errs, field.Forbidden(path, detail))
	}

	return
}

func (s *ClusterSpec) overrideImageRepositories() {
	if s != nil &&
		s.Images != nil &&
		s.Images.Repository != "" &&
		s.Network != nil &&
		s.Network.NodeLocalLoadBalancing != nil &&
		s.Network.NodeLocalLoadBalancing.EnvoyProxy != nil &&
		s.Network.NodeLocalLoadBalancing.EnvoyProxy.Image != nil {
		i := s.Network.NodeLocalLoadBalancing.EnvoyProxy.Image
		i.Image = overrideRepository(s.Images.Repository, i.Image)
	}
}

// Validate validates cluster config
func (c *ClusterConfig) Validate() (errs []error) {
	if c == nil {
		return nil
	}

	for _, err := range c.Spec.Validate() {
		errs = append(errs, fmt.Errorf("spec: %w", err))
	}

	return errs
}

// HACK: the current ClusterConfig struct holds both bootstrapping config & cluster-wide config
// this hack strips away the node-specific bootstrapping config so that we write a "clean" config to the CR
// This function accepts a standard ClusterConfig and returns the same config minus the node specific info:
// - APISpec
// - StorageSpec
// - Network.ServiceCIDR
// - Network.ClusterDomain
// - Network.ControlPlaneLoadBalancing
// - Install
func (c *ClusterConfig) GetClusterWideConfig() *ClusterConfig {
	c = c.DeepCopy()
	if c == nil {
		return nil
	}

	c.Name = "k0s"
	c.Namespace = metav1.NamespaceSystem

	if c.Spec != nil {
		c.Spec.API = nil
		c.Spec.Storage = nil
		if c.Spec.Network != nil {
			c.Spec.Network.ServiceCIDR = ""
			c.Spec.Network.ClusterDomain = ""
			c.Spec.Network.ControlPlaneLoadBalancing = nil
		}
		c.Spec.Install = nil

		stripDefaults(
			reflect.ValueOf(c.Spec).Elem(),
			reflect.ValueOf(DefaultClusterSpec()).Elem(),
		)
	}

	return c
}
