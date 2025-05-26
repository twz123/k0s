//go:build !linux

// SPDX-FileCopyrightText: 2025 k0s authors
// SPDX-License-Identifier: Apache-2.0

package iface

import (
	"errors"
	"fmt"
	"iter"
	"net"
)

func interfaceIPs(i *net.Interface) (iter.Seq[IP], error) {
	addresses, err := i.Addrs()
	if err != nil {
		return nil, fmt.Errorf("failed to list interface addresses: %w", err)
	}

	return func(yield func(IP) bool) {
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

			if !yield(genericIP(ip)) {
				return
			}
		}
	}, nil
}

type genericIP net.IP

func (ip genericIP) IP() net.IP                 { return (net.IP)(ip) }
func (ip genericIP) isSecondary() (bool, error) { return false, errors.ErrUnsupported }
