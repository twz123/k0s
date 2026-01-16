// SPDX-FileCopyrightText: 2025 k0s authors
// SPDX-License-Identifier: Apache-2.0

package iface

import (
	"iter"
	"net"
	"net/netip"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

func List() ([]Interface, error) {
	links, err := netlink.LinkList()
	if err != nil {
		return nil, err
	}

	ifaces := make([]Interface, len(links))
	for i := range links {
		ifaces[i] = &netlinkLink{links[i]}
	}

	return ifaces, nil
}

type netlinkLink struct {
	netlink.Link
}

func (l *netlinkLink) Name() string     { return l.Attrs().Name }
func (l *netlinkLink) Flags() net.Flags { return l.Attrs().Flags }
func (l *netlinkLink) isGlobal() bool   { return isGlobalInterface(l) }

func (l *netlinkLink) Addresses() (iter.Seq[Address], error) {
	addresses, err := netlink.AddrList(l, netlink.FAMILY_ALL)
	if err != nil {
		return nil, err
	}

	return func(yield func(Address) bool) {
		for i := range addresses {
			if ipNet := addresses[i].IPNet; ipNet != nil {
				if addr, ok := netip.AddrFromSlice(ipNet.IP); ok {
					if addr.IsLinkLocalUnicast() {
						addr = addr.WithZone(l.Name())
					}
					if !yield(&address{addr, addresses[i].Flags}) {
						return
					}
				}
			}
		}
	}, nil
}

type address struct {
	ip           netip.Addr
	netlinkFlags int
}

func (a *address) IP() netip.Addr { return a.ip }

func (a *address) isGlobal() bool {
	// Skip secondary IPs. This is to avoid returning VIPs as the public address.
	// https://github.com/k0sproject/k0s/issues/4664
	if a.netlinkFlags&unix.IFA_F_SECONDARY != 0 {
		return false
	}

	return isGlobalIP(a.ip)
}
