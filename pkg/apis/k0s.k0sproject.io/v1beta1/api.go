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
	"fmt"
	"net"
	"strings"

	"github.com/k0sproject/k0s/internal/pkg/iface"
	"github.com/k0sproject/k0s/internal/pkg/stringslice"

	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

// APISpec defines the settings for the K0s API
type APISpec struct {
	// Local address on which to bind the API server. Needs to be a valid IP address.
	// +required
	Address string `json:"address"`

	// The load balancer address (for k0s controllers running behind a load
	// balancer). Needs to be either a valid IP address or DNS subdomain.
	// +optional
	ExternalAddress string `json:"externalAddress,omitempty"`
	// TunneledNetworkingMode indicates if we access to KAS through konnectivity tunnel.
	// +optional
	TunneledNetworkingMode bool `json:"tunneledNetworkingMode,omitempty"`
	// Map of key-values (strings) for any extra arguments to pass down to
	// Kubernetes API server process.
	// +optional
	ExtraArgs map[string]string `json:"extraArgs,omitempty"`
	// Custom port for k0s API server to listen on. (default: 9443)
	// +optional
	// +kubebuilder:default=9443
	K0sAPIPort IPPort `json:"k0sApiPort,omitempty"`

	// Custom port for Kubernetes API server to listen on. (default: 6443)
	// +optional
	// +kubebuilder:default=6443
	Port IPPort `json:"port,omitempty"`

	// List of additional addresses to push to API servers serving the
	// certificate. Values need to be either valid IP addresses or DNS
	// subdomains.
	// +optional
	SANs []string `json:"sans,omitempty"`
}

// DefaultAPISpec default settings for api
func DefaultAPISpec() *APISpec {
	apiSpec := new(APISpec)
	SetDefaults_APISpec(apiSpec)

	// Collect all nodes addresses for SANs
	if addresses, err := iface.AllAddresses(); err == nil {
		apiSpec.SANs = addresses
	}
	if publicAddress, err := iface.FirstPublicAddress(); err == nil {
		apiSpec.Address = publicAddress
	}

	return apiSpec
}

func SetDefaults_APISpec(a *APISpec) {
	if a.ExtraArgs == nil {
		a.ExtraArgs = make(map[string]string)
	}

	a.K0sAPIPort = a.K0sAPIPort.Or(9443)
	a.Port = a.Port.Or(6443)
}

// Validate validates APISpec struct
func (a *APISpec) Validate(path *field.Path) field.ErrorList {
	allErrs := field.ErrorList{}

	validateIPAddressOrDNSName := func(path *field.Path, san string) {
		if net.ParseIP(san) != nil {
			return
		}

		details := validation.IsDNS1123Subdomain(san)
		if details == nil {
			return
		}

		allErrs = append(allErrs, field.Invalid(path, san, "neither an IP address nor a DNS name: "+strings.Join(details, "; ")))
	}

	if net.ParseIP(a.Address) == nil {
		allErrs = append(allErrs, field.Invalid(path.Child("address"), a.Address, "not an IP address"))
	}

	if a.ExternalAddress != "" {
		validateIPAddressOrDNSName(field.NewPath("externalAddress"), a.ExternalAddress)
	}

	allErrs = append(allErrs, a.K0sAPIPort.ValidateOptional(path.Child("k0sApiPort"))...)
	allErrs = append(allErrs, a.Port.ValidateOptional(path.Child("port"))...)

	sansPath := field.NewPath("sans")
	for idx, san := range a.SANs {
		validateIPAddressOrDNSName(sansPath.Index(idx), san)
	}

	defaultedCopy := *a
	SetDefaults_APISpec(&defaultedCopy)

	if defaultedCopy.K0sAPIPort == defaultedCopy.Port {
		allErrs = append(allErrs, field.Forbidden(path, "k0sApiPort and port cannot be identical"))
	}

	return allErrs
}

// APIAddress ...
func (a *APISpec) APIAddress() string {
	if a.ExternalAddress != "" {
		return a.ExternalAddress
	}
	return a.Address
}

// APIAddressURL returns kube-apiserver external URI
func (a *APISpec) APIAddressURL() string {
	return a.getExternalURIForPort(a.Port)
}

// IsIPv6String returns if ip is IPv6.
func IsIPv6String(ip string) bool {
	netIP := net.ParseIP(ip)
	return netIP != nil && netIP.To4() == nil
}

// K0sControlPlaneAPIAddress returns the controller join APIs address
func (a *APISpec) K0sControlPlaneAPIAddress() string {
	return a.getExternalURIForPort(a.K0sAPIPort)
}

func (a *APISpec) getExternalURIForPort(port IPPort) string {
	addr := a.Address
	if a.ExternalAddress != "" {
		addr = a.ExternalAddress
	}
	if IsIPv6String(addr) {
		return fmt.Sprintf("https://[%s]:%d", addr, port)
	}
	return fmt.Sprintf("https://%s:%d", addr, port)
}

// Sans return the given SANS plus all local adresses and externalAddress if given
func (a *APISpec) Sans() []string {
	sans, _ := iface.AllAddresses()
	sans = append(sans, a.Address)
	sans = append(sans, a.SANs...)
	if a.ExternalAddress != "" {
		sans = append(sans, a.ExternalAddress)
	}

	return stringslice.Unique(sans)
}
