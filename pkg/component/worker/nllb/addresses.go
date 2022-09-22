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
	"net/http"

	"go.uber.org/multierr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/watch"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/cache"
	watchtools "k8s.io/client-go/tools/watch"

	"github.com/k0sproject/k0s/internal/pkg/file"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
	"sigs.k8s.io/yaml"
)

// hostPort represents a Kubernetes API server address to be used for node-local
// load balancing.
type hostPort struct {
	Host string `json:"host"`
	Port uint16 `json:"port"`
}

// watchEndpointsResource watches the default/kubernetes Endpoints resource,
// calling callback whenever changes are observed.
func watchEndpointsResource(ctx context.Context, log logrus.FieldLogger, client corev1client.CoreV1Interface, callback func(*corev1.Endpoints) error) error {
	endpoints := client.Endpoints("default")
	fieldSelector := fields.OneTermEqualSelector(metav1.ObjectNameField, "kubernetes").String()

listAndWatch:
	for {
		initial, err := endpoints.List(ctx, metav1.ListOptions{FieldSelector: fieldSelector})
		if err != nil {
			return err
		}
		if len(initial.Items) != 1 {
			return fmt.Errorf("didn't find exactly one Endpoints object for API servers, but %d", len(initial.Items))
		}
		if err := callback(&initial.Items[0]); err != nil {
			return err
		}

		changes, err := watchtools.NewRetryWatcher(initial.ResourceVersion, &cache.ListWatch{
			WatchFunc: func(opts metav1.ListOptions) (watch.Interface, error) {
				opts.FieldSelector = fieldSelector
				return endpoints.Watch(ctx, opts)
			},
		})
		if err != nil {
			return err
		}
		defer changes.Stop()

		for {
			select {
			case event, ok := <-changes.ResultChan():
				if !ok {
					return errors.New("result channel closed unexpectedly")
				}

				switch event.Type {
				case watch.Added, watch.Modified:
					if ep, ok := event.Object.(*corev1.Endpoints); ok && ep.GetName() == "kubernetes" && ep.GetNamespace() == "default" {
						if err := callback(ep); err != nil {
							return err
						}
						continue
					}
					fallthrough

				case watch.Deleted:
					return fmt.Errorf("unexpected watch event: %s %#+v", event.Type, event.Object)
				case watch.Bookmark:
					log.Debugf("Bookmark received while watching API servers: %#+v", event.Object)
				case watch.Error:
					err := apierrors.FromObject(event.Object)
					var statusErr *apierrors.StatusError
					if errors.As(err, &statusErr) && statusErr.ErrStatus.Code == http.StatusGone {
						log.WithError(err).Debug("Resource too old while watching API serverss, starting over")
						continue listAndWatch
					}

					return fmt.Errorf("watch error: %w", err)
				}

			case <-ctx.Done():
				return nil
			}
		}
	}
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
	addresses := []hostPort{}

	var warnings error
	for sIdx, subset := range endpoints.Subsets {
		var ports []uint16
		for _, port := range subset.Ports {
			// FIXME: is a more sophisticated port detection required?
			// E.g. does the service object need to be inspected?
			if port.Protocol == corev1.ProtocolTCP && port.Name == "https" {
				if port.Port > 0 && port.Port <= math.MaxUint16 {
					ports = append(ports, uint16(port.Port))
				}
			}
		}

		if len(ports) < 1 {
			warnings = multierr.Append(warnings, fmt.Errorf("no suitable ports found in subset %d: %+#v", sIdx, subset.Ports))
			continue
		}

		for aIdx, address := range subset.Addresses {
			host := address.IP
			if host == "" {
				host = address.Hostname
			}
			if host == "" {
				warnings = multierr.Append(warnings, fmt.Errorf("failed to get host from address %d/%d: %+#v", sIdx, aIdx, address))
				continue
			}

			for _, port := range ports {
				addresses = append(addresses, hostPort{host, port})
			}
		}
	}

	if len(addresses) < 1 {
		// Never update the API servers with an empty list. This cannot
		// be right in any case, and would never recover.
		return nil, multierr.Append(errors.New("no API server addresses discovered"), warnings)
	}

	return addresses, nil
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
