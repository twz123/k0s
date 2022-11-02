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

package nllb

import (
	"context"
	"errors"
	"fmt"
	"math"

	"github.com/k0sproject/k0s/internal/pkg/file"
	"github.com/k0sproject/k0s/pkg/kubernetes/watch"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/validation/field"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"sigs.k8s.io/yaml"

	"go.uber.org/multierr"
	"golang.org/x/sync/errgroup"
)

// hostPort represents a Kubernetes API server address to be used for node-local
// load balancing.
type hostPort struct {
	Host string `json:"host"`
	Port uint16 `json:"port"`
}

// watchEndpointsResource watches the default/kubernetes Endpoints resource,
// calling callback whenever changes are observed.
func watchEndpointsResource(ctx context.Context, lastObservedVersion string, client corev1client.CoreV1Interface, callback func(*corev1.Endpoints) error) (string, error) {
	err := watch.Endpoints(client.Endpoints("default")).WithObjectName("kubernetes").Until(
		ctx, func(endpoints *corev1.Endpoints) (bool, error) {
			if err := callback(endpoints); err != nil {
				return false, err
			}

			lastObservedVersion = endpoints.ResourceVersion
			return false, nil
		},
	)

	return lastObservedVersion, err
}

func updateAPIServerAddresses(ctx context.Context, endpoints *corev1.Endpoints, updates chan<- podStateUpdateFunc, backupFilePath string) error {
	addresses, err := extractAPIServerAddresses(endpoints)
	if err != nil {
		return fmt.Errorf("failed to extract API server addresses from Endpoints resource: %w", err)
	}

	var eg errgroup.Group
	eg.Go(func() error {
		apiServerBytes, err := yaml.Marshal(addresses)
		if err != nil {
			return err
		}

		return file.WriteContentAtomically(backupFilePath, apiServerBytes, 0644)
	})

	updateFunc := func(state *podState) {
		state.LoadBalancer.UpstreamServers = addresses
	}

	cancelled := false
	select {
	case updates <- updateFunc:
	case <-ctx.Done():
		cancelled = true
	}

	if err := eg.Wait(); err != nil {
		return err
	}
	if cancelled {
		return ctx.Err()
	}
	return nil
}

func extractAPIServerAddresses(endpoints *corev1.Endpoints) ([]hostPort, error) {
	var warnings error
	apiServers := []hostPort{}

	for sIdx, subset := range endpoints.Subsets {
		var ports []uint16
		for pIdx, port := range subset.Ports {
			// FIXME: is a more sophisticated port detection required?
			// E.g. does the service object need to be inspected?
			if port.Protocol != corev1.ProtocolTCP || port.Name != "https" {
				continue
			}

			if port.Port < 0 || port.Port > math.MaxUint16 {
				path := field.NewPath("subsets").Index(sIdx).Child("ports").Index(pIdx).Child("port")
				warning := field.Invalid(path, port.Port, "out of range")
				warnings = multierr.Append(warnings, warning)
				continue
			}

			ports = append(ports, uint16(port.Port))
		}

		if len(ports) < 1 {
			path := field.NewPath("subsets").Index(sIdx)
			warning := field.Forbidden(path, "no suitable TCP/https ports found")
			warnings = multierr.Append(warnings, warning)
			continue
		}

		for aIdx, address := range subset.Addresses {
			host := address.IP
			if host == "" {
				host = address.Hostname
			}
			if host == "" {
				path := field.NewPath("addresses").Index(aIdx)
				warning := field.Forbidden(path, "neither ip nor hostname specified")
				warnings = multierr.Append(warnings, warning)
				continue
			}

			for _, port := range ports {
				apiServers = append(apiServers, hostPort{host, port})
			}
		}
	}

	if len(apiServers) < 1 {
		// Never update the API servers with an empty list. This cannot
		// be right in any case, and would never recover.
		return nil, multierr.Append(errors.New("no API server addresses discovered"), warnings)
	}

	return apiServers, nil
}

// func parseHostPort(address string, defaultPort uint16) (*nllbHostPort, error) {
// 	host, portStr, err := net.SplitHostPort(address)
// 	if err != nil {
// 		if defaultPort != 0 {
// 			addrErr := &net.AddrError{}
// 			if errors.As(err, &addrErr) && addrErr.Err == "missing port in address" {
// 				return &nllbHostPort{addrErr.Addr, defaultPort}, nil
// 			}
// 		}

// 		return nil, err
// 	}

// 	port, err := strconv.ParseUint(portStr, 10, 16)
// 	if err != nil {
// 		return nil, fmt.Errorf("invalid port number: %q: %w", portStr, err)
// 	}

// 	return &nllbHostPort{host, uint16(port)}, nil
// }
