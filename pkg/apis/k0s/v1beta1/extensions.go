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
	"errors"
	"fmt"

	"helm.sh/helm/v3/pkg/chartutil"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var _ Validateable = (*ClusterExtensions)(nil)

// ClusterExtensions specifies cluster extensions
type ClusterExtensions struct {
	//+kubebuilder:deprecatedversion:warning="storage is deprecated and will be ignored in 1.30. https://docs.k0sproject.io/stable/examples/openebs".
	Storage *StorageExtension `json:"storage"`
	Helm    *HelmExtensions   `json:"helm"`
}

// HelmExtensions specifies settings for cluster helm based extensions
type HelmExtensions struct {
	// Currently unused.
	ConcurrencyLevel int          `json:"concurrencyLevel,omitempty"`
	Repositories     Repositories `json:"repositories,omitempty"`
	Charts           Charts       `json:"charts,omitempty"`
}

func DefaultHelmExtensions() *HelmExtensions {
	return &HelmExtensions{
		// ConcurrencyLevel: 5, // Currently unused.
	}
}

// A list of Helm chart repositories.
// +listType=map
// +listMapKey=name
type Repositories []Repository

// A list of Helm charts.
// +listType=map
// +listMapKey=name
type Charts []Chart

// Validate performs validation
func (rs Repositories) Validate() []error {
	var errs []error
	for _, r := range rs {
		if err := r.Validate(); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return errs
	}
	return nil
}

// Validate performs validation
func (cs Charts) Validate() []error {
	var errs []error
	for _, c := range cs {
		if err := c.Validate(); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return errs
	}
	return nil
}

// Validate performs validation
func (he *HelmExtensions) Validate() []error {
	var errs []error
	if rErrs := he.Repositories.Validate(); rErrs != nil {
		errs = append(errs, rErrs...)
	}
	if cErrs := he.Charts.Validate(); cErrs != nil {
		errs = append(errs, cErrs...)
	}
	if len(errs) > 0 {
		return errs
	}
	return nil
}

// Chart single helm addon
type Chart struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	ChartName string `json:"chartname"`
	Version   string `json:"version"`
	Values    string `json:"values"`
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	TargetNS string `json:"namespace"`
	// Timeout specifies the timeout for how long to wait for the chart installation to finish.
	// A duration string is a sequence of decimal numbers, each with optional fraction and a unit suffix, such as "300ms" or "2h45m". Valid time units are "ns", "us" (or "µs"), "ms", "s", "m", "h".
	Timeout metav1.Duration `json:"timeout,omitempty"`
	Order   int             `json:"order,omitempty"`
}

// ManifestFileName returns filename to use for the crd manifest
func (c *Chart) ManifestFileName() string {
	return fmt.Sprintf("%d_helm_extension_%s.yaml", c.Order, c.Name)
}

// Validate performs validation
func (c *Chart) Validate() error {
	if c.Name == "" {
		return errors.New("chart must have Name field not empty")
	}
	if err := chartutil.ValidateReleaseName(c.Name); err != nil {
		return err
	}
	if c.ChartName == "" {
		return errors.New("chart must have ChartName field not empty")
	}
	if c.TargetNS == "" {
		return errors.New("chart must have TargetNS field not empty")
	}
	return nil
}

// Repository describes single repository entry. Fields map to the CLI flags for the "helm add" command
type Repository struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	URL      string `json:"url"`
	CAFile   string `json:"caFile,omitempty"`
	CertFile string `json:"certFile,omitempty"`
	Insecure bool   `json:"insecure,omitempty"`
	KeyFile  string `json:"keyfile,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
}

// Validate performs validation
func (r *Repository) Validate() error {
	if r.Name == "" {
		return errors.New("repository must have Name field not empty")
	}
	if r.URL == "" {
		return errors.New("repository must have URL field not empty")
	}
	return nil
}

// Validate stub for Validateable interface
func (e *ClusterExtensions) Validate() []error {
	if e == nil {
		return nil
	}
	var errs []error
	if e.Helm != nil {
		errs = append(errs, e.Helm.Validate()...)
	}
	if e.Storage != nil {
		errs = append(errs, e.Storage.Validate()...)
	}
	return errs
}

func DefaultStorageExtension() *StorageExtension {
	return &StorageExtension{
		Type: ExternalStorage,
	}
}

// DefaultExtensions default values
func DefaultExtensions() *ClusterExtensions {
	return &ClusterExtensions{
		Storage: DefaultStorageExtension(),
		Helm:    DefaultHelmExtensions(),
	}
}
