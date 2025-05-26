// SPDX-FileCopyrightText: 2021 k0s authors
// SPDX-License-Identifier: Apache-2.0

package iface

import (
	"fmt"
	"net"
	"strings"

	"github.com/sirupsen/logrus"
)

type IP interface {
	IP() net.IP

	isSecondary() (bool, error)
}

// AllAddresses returns a list of all network addresses on a node
func AllAddresses() ([]string, error) {
	addresses, err := CollectAllIPs()
	if err != nil {
		return nil, err
	}
	strings := make([]string, len(addresses))
	for i, addr := range addresses {
		strings[i] = addr.String()
	}
	return strings, nil
}

// CollectAllIPs returns a list of all network addresses on a node
func CollectAllIPs() (addresses []net.IP, err error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil, fmt.Errorf("failed to list network interfaces: %w", err)
	}

	for _, a := range addrs {
		// check the address type and skip if loopback
		if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if ipnet.IP.To4() != nil || ipnet.IP.To16() != nil {
				addresses = append(addresses, ipnet.IP)
			}
		}
	}

	return addresses, nil
}

// FirstPublicAddress return the first found non-local IPv4 address that's not part of pod network
// if any interface does not have any IPv4 address then return the first found non-local IPv6 address
func FirstPublicAddress() (string, error) {
	ifs, err := net.Interfaces()
	if err != nil {
		return "127.0.0.1", fmt.Errorf("failed to list network interfaces: %w", err)
	}

	var ipv6 net.IP
	for i := range ifs {
		i := &ifs[i]
		switch {
		case isCNIInterface(i): // Skip all CNI related interfaces
			continue
		case i.Name == "dummyvip0": // Skip k0s CPLB interface
			continue
		}

		ips, err := interfaceIPs(i)
		if err != nil {
			logrus.WithError(err).Warn("Skipping network interface ", i.Name)
			continue
		}
		for ip := range ips {
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
