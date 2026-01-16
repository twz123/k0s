// SPDX-FileCopyrightText: 2021 k0s authors
// SPDX-License-Identifier: Apache-2.0

package iface

import (
	"errors"
	"fmt"
	"iter"
	"net"
	"net/netip"
	"strings"
)

type Interface interface {
	Name() string
	Flags() net.Flags
	Addresses() (iter.Seq[Address], error)

	netItem
}

type Address interface {
	IP() netip.Addr

	netItem
}

type netItem interface {
	isGlobal() bool
}

func All() iter.Seq2[Interface, error] {
	return func(yield func(Interface, error) bool) {
		ifaces, err := List()
		if err != nil {
			yield(nil, err)
			return
		}

		for _, i := range ifaces {
			if !yield(i, nil) {
				return
			}
		}
	}
}

func AllAddresses() iter.Seq2[Address, error] {
	return Addresses(All())
}

func Addresses(interfaces iter.Seq2[Interface, error]) iter.Seq2[Address, error] {
	return func(yield func(Address, error) bool) {
		for i, err := range interfaces {
			if err != nil {
				if !yield(nil, err) {
					return
				}
				continue
			}

			addrs, err := i.Addresses()
			if err != nil {
				if !yield(nil, fmt.Errorf("failed to get addresses of network interface %s: %w", i.Name(), err)) {
					return
				}
				continue
			}

			for addr := range addrs {
				if !yield(addr, nil) {
					return
				}
			}
		}
	}
}

func AllGlobalAddresses() iter.Seq2[Address, error] {
	return global(Addresses(global(All())))
}

func FirstAddresses(addresses iter.Seq2[Address, error]) (v4, v6 netip.Addr, _ error) {
	var errs []error
	for addr, err := range addresses {
		if err != nil {
			errs = append(errs, err)
			continue
		}

		if ip := addr.IP(); ip.Is4() {
			if !v4.IsValid() {
				v4 = ip
				if v6.IsValid() {
					break
				}
			}
		} else if ip.Is6() {
			if !v6.IsValid() {
				v6 = ip
				if v4.IsValid() {
					break
				}
			}
		}
	}

	return v4, v6, errors.Join(errs...)
}

func global[T netItem](seq iter.Seq2[T, error]) iter.Seq2[T, error] {
	return func(yield func(T, error) bool) {
		for item, err := range seq {
			if err == nil && !item.isGlobal() {
				continue
			}
			if !yield(item, err) {
				return
			}
		}
	}
}

func isGlobalInterface[T Interface](i T) bool {
	switch {
	case i.Flags()&(net.FlagUp) == 0: // Skip disabled interfaces
	case i.Flags()&(net.FlagLoopback|net.FlagPointToPoint) != 0: // Skip loopback and P2P interfaces
	case isCNIInterface(i.Name()): // Skip all CNI related interfaces
	case i.Name() == "dummyvip0": // Skip k0s CPLB interface
	default: // All other interfaces are considered "global"
		return true
	}

	return false
}

func isCNIInterface(name string) bool {
	switch {
	case strings.HasPrefix(name, "veth"): // kube-router pod CNI interfaces
	case strings.HasPrefix(name, "cali"): // Calico pod CNI interfaces
	case name == "kube-bridge": // kube-router CNI interface
	case name == "vxlan.calico": // Calico CNI interface
	default: // All other interfaces are not considered to be related to CNI.
		return false
	}

	return true
}

func isGlobalIP(ip netip.Addr) bool {
	return ip.IsGlobalUnicast()
}
