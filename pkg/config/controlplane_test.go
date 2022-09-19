/*
Copyright 2022 k0s authors

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

package config

import (
	"net"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestStorageSpec_IsJoinable(t *testing.T) {
	tests := []struct {
		name    string
		storage StorageSpec
		want    bool
	}{
		{
			name: "Etcd",
			storage: StorageSpec{
				Type: "Etcd",
			},
			want: true,
		},
		{
			name: "ExternalEtcd",
			storage: StorageSpec{
				Type: "ExternalEtcd",
				ExternalEtcd: &ExternalEtcdSpec{
					Endpoints:  []url.URL{{Scheme: "https", Host: "192.168.10.2:2379"}},
					EtcdPrefix: "k0s-tenant-1",
				},
			},
			want: false,
		},
		{
			name: "kine-sqlite",
			storage: StorageSpec{
				Type: "Kine",
				Kine: &KineSpec{
					DataSource: "sqlite://foobar",
				},
			},
			want: false,
		},
		{
			name: "kine-mysql",
			storage: StorageSpec{
				Type: "Kine",
				Kine: &KineSpec{
					DataSource: "mysql://foobar",
				},
			},
			want: true,
		},
		{
			name: "kine-postgres",
			storage: StorageSpec{
				Type: "Kine",
				Kine: &KineSpec{
					DataSource: "postgres://foobar",
				},
			},
			want: true,
		},
		{
			name: "kine-unknown",
			storage: StorageSpec{
				Type: "Kine",
				Kine: &KineSpec{
					DataSource: "unknown://foobar",
				},
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.storage.IsJoinable(); got != tt.want {
				assert.Equal(t, tt.want, got, "IsJoinable() mismatch")
			}
		})
	}
}

func TestNetworkServiceSpec_URI(t *testing.T) {
	for _, test := range []struct {
		name             string
		ip               net.IP
		port             uint16
		externalAddress  string
		expectedHostPort string
	}{
		{
			name:             "OnlyIP",
			ip:               net.IPv4(127, 10, 10, 1),
			expectedHostPort: "127.10.10.1",
		},
		{
			name: "IPAndPort",
			ip:   net.IPv4(127, 10, 10, 1), port: 1337,
			expectedHostPort: "127.10.10.1:1337",
		},
		{
			name:             "ExternalAddress",
			externalAddress:  "yolo",
			expectedHostPort: "yolo",
		},
		{
			name:            "ExternalAddressWithPort",
			externalAddress: "yolo", port: 1337,
			expectedHostPort: "yolo:1337",
		},
		{
			name:            "IPv6ExternalAddressWithPort",
			externalAddress: "fe80::42:97ff:fe22:cd8", port: 1337,
			expectedHostPort: "[fe80::42:97ff:fe22:cd8]:1337",
		},
	} {
		expected := &url.URL{
			Scheme: "https",
			Host:   test.expectedHostPort,
		}

		underTest := NetworkService{
			BindAddress: NetAddress{
				IP:   test.ip,
				Port: IPPort(test.port),
			},
			ExternalAddress: test.externalAddress,
		}

		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, expected, underTest.URL())
		})
	}
}

func TestControlPlaneNetworkSpec_DNSAddress(t *testing.T) {
	for _, test := range []struct {
		name     string
		v4, v6   *net.IPNet
		expected any // net.IP or string as error message
	}{{
		"NoCIDRs", nil, nil, `neither V4 nor V6 CIDR given`,
	}, {
		"V4InV6", nil, cidr(net.IP{10, 96, 0, 0}, 24),
		"CIDR 10.96.0.0/24: not a valid 16 byte network mask in ",
	}, {
		"V6InV4", cidr(net.IPv6loopback, 64), nil,
		"CIDR ::1/64: not a valid 4 byte network address in ",
	}, {
		"DefaultCIDR",
		cidr(net.IP{10, 96, 0, 0}, 24), nil,
		net.IP{10, 96, 0, 10},
	}, {
		"NarrowCIDR",
		cidr(net.IP{10, 96, 0, 248}, 30), nil,
		net.IP{10, 96, 0, 250},
	}, {
		"IPV4TooNarrow", cidr(net.IP{10, 96, 0, 0}, 31), nil,
		"CIDR 10.96.0.0/31 is too narrow, need at least two bits in the address space",
	}, {
		"IPv6",
		nil, cidr(net.IP{0x2a, 0x01, 0xc, 0x23, 0x71, 0x31, 0xa4, 0, 0, 0, 0, 0, 0, 0, 0, 0}, 64),
		net.IP{0x2a, 0x01, 0xc, 0x23, 0x71, 0x31, 0xa4, 0, 0, 0, 0, 0, 0, 0, 0, 0xa},
	}, {
		"NarrowIPv6",
		nil, cidr(net.IP{0x2a, 0x01, 0xc, 0x22, 0xbc, 0x93, 0x35, 0x00, 0xe6, 0x78, 0xe4, 0x6f, 0x65, 0xc3, 0xa2, 0xa0}, 126),
		net.IP{0x2a, 0x01, 0xc, 0x22, 0xbc, 0x93, 0x35, 0x00, 0xe6, 0x78, 0xe4, 0x6f, 0x65, 0xc3, 0xa2, 0xa2},
	}, {
		"IPV6TooNarrow", nil, cidr(net.IPv6loopback, 127),
		"CIDR ::1/127 is too narrow, need at least two bits in the address space",
	},
	} {
		underTest := ControlPlaneNetworkSpec{
			ServiceCIDRs: CIDRSpec{V4: test.v4, V6: test.v6},
		}

		t.Run(test.name, func(t *testing.T) {
			dns, err := underTest.DNSAddress()
			if msg, ok := test.expected.(string); ok {
				if assert.Error(t, err) {
					assert.Contains(t, err.Error(), msg)
				}
			} else if assert.NoError(t, err) {
				assert.Equal(t, test.expected, dns)
			}
		})
	}
}

func cidr(ip net.IP, prefixLen byte) *net.IPNet {
	return &net.IPNet{
		IP:   ip,
		Mask: net.CIDRMask(int(prefixLen), 8*len(ip)),
	}
}
