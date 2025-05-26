// SPDX-FileCopyrightText: 2025 k0s authors
// SPDX-License-Identifier: Apache-2.0

package iface

import (
	"fmt"
	"iter"
	"net"

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

func interfaceIPs(i *net.Interface) (iter.Seq[IP], error) {
	link, err := netlink.LinkByName(i.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to get link by name: %w", err)
	}

	addresses, err := netlink.AddrList(link, netlink.FAMILY_ALL)
	if err != nil {
		return nil, fmt.Errorf("failed to list IP addresses: %w", err)
	}

	return func(yield func(IP) bool) {
		for i := range addresses {
			a := &addresses[i]
			if a.IPNet != nil {
				if !yield((*netlinkIP)(a)) {
					return
				}
			}
		}
	}, nil
}

type netlinkIP netlink.Addr

func (ip *netlinkIP) IP() net.IP                 { return ip.IPNet.IP }
func (ip *netlinkIP) isSecondary() (bool, error) { return ip.Flags&unix.IFA_F_SECONDARY != 0, nil }
