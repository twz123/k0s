//go:build !linux

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
