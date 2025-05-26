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

package iface

import (
	"fmt"
	"iter"
	"net"
	"strings"

	"github.com/sirupsen/logrus"
)

type IP interface {
	IP() net.IP

	isSecondary() (bool, error)
}

// AllAddresses returns a list of all network addresses on a node
func AllAddresses() (addresses []string, _ error) {
	ips, err := EnumerateAllIPs()
	if err != nil {
		return nil, err
	}

	for ip := range ips {
		addresses = append(addresses, ip.IP().String())
	}

	return addresses, nil
}

// AllAddresses returns a list of all network addresses on a node
func EnumerateAllIPs() (iter.Seq[IP], error) {
	ips, err := allInterfaceIPs(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to enumerate IPs of network interfaces: %w", err)
	}

	return func(yield func(IP) bool) {
		for ip, err := range ips {
			if err != nil {
				logrus.WithError(err).Warn("Failed to enumerate some IPs")
				continue
			}

			addr := ip.IP()
			switch {
			case addr == nil, addr.IsUnspecified(): // Skip unspecified IPs
			case addr.IsLoopback(), addr.IsInterfaceLocalMulticast(): // Skip interface-local IPs
			case !yield(ip):
				return
			}
		}
	}, nil
}

// FirstPublicAddress return the first found non-local IPv4 address that's not part of pod network
// if any interface does not have any IPv4 address then return the first found non-local IPv6 address
func FirstPublicAddress() (string, error) {
	isNonLocalInterface := func(i *net.Interface) bool {
		switch {
		case isCNIInterface(i): // Skip all CNI interfaces
			return false
		case i.Name == "dummyvip0": // Skip the k0s CPLB interface
			return false
		default: // all others are supposedly "non-local"
			return true
		}
	}

	ips, err := allInterfaceIPs(isNonLocalInterface)
	if err != nil {
		return "127.0.0.1", fmt.Errorf("failed to enumerate IPs of network interfaces: %w", err)
	}

	var ipv6 net.IP
	for ip, err := range ips {
		if err != nil {
			logrus.WithError(err).Warn("Failed to enumerate some IPs")
			continue
		}

		addr := ip.IP()
		switch {
		// Skip unspecified IPs
		case addr == nil, addr.IsUnspecified():
			continue
		// Skip multicast IPs
		case addr.IsMulticast():
			continue
		// Skip interface-local IPs (interface-local multicast already excluded above)
		case addr.IsLoopback():
			continue
		}

		// Skip secondary IPs. This is to avoid returning VIPs as the public address.
		// https://github.com/k0sproject/k0s/issues/4664
		if secondary, err := ip.isSecondary(); err == nil && secondary {
			continue
		}

		if ip := addr.To4(); ip != nil {
			return ip.String(), nil
		}
		if ipv6 == nil {
			if ip := addr.To16(); ip != nil {
				ipv6 = ip
			}
		}
	}

	if ipv6 != nil {
		return ipv6.String(), nil
	}

	logrus.Warn("failed to find any non-local, non podnetwork addresses on host, defaulting public address to 127.0.0.1")
	return "127.0.0.1", nil
}

func isCNIInterface(i *net.Interface) bool {
	switch {
	case strings.HasPrefix(i.Name, "veth"): // kube-router pod CNI interfaces
	case strings.HasPrefix(i.Name, "cali"): // Calico pod CNI interfaces
	case i.Name == "kube-bridge": // kube-router CNI interface
	case i.Name == "vxlan.calico": // Calico CNI interface
	default: // All other interfaces are not considered to be related to CNI.
		return false
	}

	return true
}

func allInterfaceIPs(isInterfaceAllowed func(*net.Interface) bool) (iter.Seq2[IP, error], error) {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("failed to list network interfaces: %w", err)
	}

	return func(yield func(IP, error) bool) {
		for i := range ifs {
			i := &ifs[i]

			if isInterfaceAllowed != nil && !isInterfaceAllowed(i) {
				continue
			}

			ips, err := interfaceIPs(i)
			if err != nil {
				err := fmt.Errorf("failed to enumerate IPs of network interface %s: %w", i.Name, err)
				if !yield(nil, err) {
					return
				}
				continue
			}

			for ip := range ips {
				if !yield(ip, nil) {
					return
				}
			}
		}
	}, nil
}
