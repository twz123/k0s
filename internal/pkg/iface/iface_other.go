//go:build !linux

// SPDX-FileCopyrightText: 2025 k0s authors
// SPDX-License-Identifier: Apache-2.0

package iface

import (
	"iter"
	"net"
	"net/netip"
	"runtime"
	"strconv"
)

func List() ([]Interface, error) {
	netInterfaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}

	interfaces := make([]Interface, len(netInterfaces))
	for i := range netInterfaces {
		interfaces[i] = &netInterface{netInterfaces[i]}
	}

	return interfaces, nil
}

type netInterface struct {
	inner net.Interface
}

func (i *netInterface) Name() string     { return i.inner.Name }
func (i *netInterface) Flags() net.Flags { return i.inner.Flags }
func (i *netInterface) isGlobal() bool   { return isGlobalInterface(i) }

func (i *netInterface) Addresses() (iter.Seq[Address], error) {
	addresses, err := i.inner.Addrs()
	if err != nil {
		return nil, err
	}

	return func(yield func(Address) bool) {
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

			if addr, ok := netip.AddrFromSlice(ip); ok {
				if addr.IsLinkLocalUnicast() {
					if runtime.GOOS == "windows" {
						addr = addr.WithZone(strconv.Itoa(i.inner.Index))
					} else {
						addr = addr.WithZone(i.inner.Name)
					}
				}
				if !yield(address(addr)) {
					return
				}
			}
		}
	}, nil
}

type address netip.Addr

func (a address) IP() netip.Addr { return (netip.Addr)(a) }
func (a address) isGlobal() bool { return isGlobalIP(a.IP()) }
