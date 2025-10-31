/*
Copyright 2025 k0s authors

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

	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

func All() (iter.Seq[Interface], error) {
	links, err := netlink.LinkList()
	if err != nil {
		return nil, err
	}

	return func(yield func(Interface) bool) {
		for i := range links {
			if !yield(&netlinkLink{links[i]}) {
				return
			}
		}
	}, nil
}

type netlinkLink struct {
	netlink.Link
}

func (l netlinkLink) Name() string {
	return l.Attrs().Name
}

func (link *netlinkLink) IPs() (iter.Seq[IP], error) {
	attrs := link.Attrs()
	fmt.Printf("Name: %s (%T)\n", attrs.Name, link)
	fmt.Printf("  Type: %s\n", link.Type())
	fmt.Printf("  MasterIndex: %d\n", link.Attrs().MasterIndex)
	fmt.Printf("  Namespace: %+v\n", link.Attrs().NetNsID)

	// If the link is a veth, get peer name
	if veth, ok := link.Link.(*netlink.Veth); ok {
		fmt.Printf("  PeerName: %s\n", veth.PeerName)
	}
	fmt.Println()

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
