// SPDX-FileCopyrightText: 2021 k0s authors
// SPDX-License-Identifier: Apache-2.0

package iface

import (
	"fmt"
	"net"
	"strings"

	"github.com/sirupsen/logrus"
)

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
	ipv6addr := ""
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
			// check the address type and skip if loopback
			if !ip.IsLoopback() {
				if ip.To4() != nil {
					return ip.String(), nil
				}
				if ip.To16() != nil && ipv6addr == "" {
					ipv6addr = ip.String()
				}
			}
		}
	}
	if ipv6addr != "" {
		return ipv6addr, nil
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
