//go:build !linux

// SPDX-FileCopyrightText: 2025 k0s authors
// SPDX-License-Identifier: Apache-2.0

package iface

import (
	"fmt"
	"iter"
	"net"
)

func interfaceIPs(i net.Interface) (iter.Seq[net.IP], error) {
	addresses, err := i.Addrs()
	if err != nil {
		return nil, fmt.Errorf("failed to list interface addresses: %w", err)
	}

	return func(yield func(net.IP) bool) {
		for _, a := range addresses {
			var ip net.IP
			switch a := a.(type) {
			case *net.IPNet:
				ip = a.IP
			case *net.IPAddr: // Windows Anycast
				ip = a.IP
			default:
				continue
			}

			if !yield(ip) {
				return
			}
		}
	}, nil
}
