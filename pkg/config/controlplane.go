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
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/k0sproject/k0s/pkg/apis/k0s.k0sproject.io/v1beta1"
)

type ControlPlaneSpec struct {
	Install   v1beta1.InstallSpec     `json:"install"`
	APIServer APIServerSpec           `json:"apiServer"`
	K0sAPI    NetworkService          `json:"k0sApi"`
	Network   ControlPlaneNetworkSpec `json:"network"`
	Storage   StorageSpec             `json:"storage"`
}

type NetworkService struct {
	BindAddress     NetAddress `json:"bindAddress"`
	ExternalAddress string     `json:"externalAddress,omitempty"`

	// Additional subject alternative names to be added to the service's TLS certificate.
	AdditionalSANs []string `json:"additionalSans,omitempty"`
}

type APIServerSpec struct {
	NetworkService         `json:",omitempty,inline"`
	ExtraArgs              map[string]string `json:"extraArgs,omitempty"`
	TunneledNetworkingMode bool              `json:"tunneledNetworkingMode,omitempty"`
}

type ControlPlaneNetworkSpec struct {
	ClusterDomain string   `json:"clusterDomain"`
	ServiceCIDRs  CIDRSpec `json:"serviceCidrs"`
}

type CIDRSpec struct {
	V4 *net.IPNet `json:"v4,omitempty"`
	V6 *net.IPNet `json:"v6,omitempty"`
}

type StorageSpec struct {
	Type         StorageType       `json:"type"`
	Etcd         *EtcdSpec         `json:"etcd,omitempty"`
	ExternalEtcd *ExternalEtcdSpec `json:"externalEtcd,omitempty"`
	Kine         *KineSpec         `json:"kine,omitempty"`
}

type StorageType string

// Supported storage types.
const (
	// Internal etcd storage
	EtcdStorageType StorageType = "Etcd"
	// External etcd storage
	ExternalEtcdStorageType StorageType = "ExternalEtcd"
	// Internal kine storage
	KineStorageType StorageType = "Kine"
)

type ExternalEtcdSpec struct {
	// Endpoints of external etcd cluster used to connect by k0s.
	Endpoints []url.URL `json:"endpoints,omitempty"`

	// EtcdPrefix is a prefix to prepend to all resource paths in etcd
	EtcdPrefix string `json:"etcdPrefix,omitempty"`

	// TLS contains the TLS configuration for the cluster, if any.
	TLS *EtcdTLS `json:"tls,omitempty"`
}

// EtcdTLS contains the file paths to set up a secured connection to an etcd cluster.
type EtcdTLS struct {
	// CertFile is the host path to a file with TLS certificate for etcd client
	CertFile string `json:"certFile"`

	// KeyFile is the host path to a file with TLS key for etcd client
	KeyFile string `json:"keyFile"`

	// CAFile is the host path to a file with CA certificate
	CAFile string `json:"caFile"`
}

type EtcdSpec struct {
	BindAddress NetAddress `json:"bindAddress"`
	PeerAddress string     `json:"peerAddress"`
	PeerPort    IPPort     `json:"peerPort,omitempty"`

	// Additional subject alternative names to be added to the service's TLS certificate.
	AdditionalSANs []string `json:"additionalSans,omitempty"`
}

type KineSpec struct {
	DataSource string `json:"dataSource"`
}

type NetAddress struct {
	IP   net.IP `json:"ip"`
	Port IPPort `json:"port"`
}

type IPPort uint16

// DNSAddress calculates the 10th address of configured service CIDR block.
func (n *ControlPlaneNetworkSpec) DNSAddress() (addr net.IP, err error) {
	var ipNet *net.IPNet
	var bitsInAddressSpace int
	if n.ServiceCIDRs.V4 != nil {
		ipNet = n.ServiceCIDRs.V4
		addr, bitsInAddressSpace, err = getNetworkAddress(ipNet, net.IP.To4, net.IPv4len)
	} else if n.ServiceCIDRs.V6 != nil {
		ipNet = n.ServiceCIDRs.V6
		addr, bitsInAddressSpace, err = getNetworkAddress(ipNet, net.IP.To16, net.IPv6len)
	} else {
		return nil, fmt.Errorf("neither V4 nor V6 CIDR given")
	}
	if err != nil {
		return nil, fmt.Errorf("CIDR %s: %w", ipNet, err)
	}

	if bitsInAddressSpace > 3 {
		addr[len(addr)-1] += 10
	} else if bitsInAddressSpace > 1 {
		addr[len(addr)-1] += 2
	} else {
		return nil, fmt.Errorf("CIDR %s is too narrow, need at least two bits in the address space", ipNet)
	}

	return addr, nil
}

// IsJoinable returns true only if the storage config is such that another
// controller can join the cluster.
func (s *StorageSpec) IsJoinable() bool {
	switch s.Type {
	case EtcdStorageType:
		return s.Etcd.IsJoinable()
	case ExternalEtcdStorageType:
		return s.ExternalEtcd.IsJoinable()
	case KineStorageType:
		return s.Kine.IsJoinable()
	default:
		return false
	}
}

func (*EtcdSpec) IsJoinable() bool {
	return true
}

func (e *EtcdSpec) URL() *url.URL {
	return &url.URL{Scheme: "https", Host: e.BindAddress.String()}
}

func (e *EtcdSpec) PeerURL() *url.URL {
	peerService := NetworkService{
		BindAddress:     NetAddress{Port: e.PeerPort.Or(2380)},
		ExternalAddress: e.PeerAddress,
	}

	return peerService.URL()
}

func (*ExternalEtcdSpec) IsJoinable() bool {
	return false
}

func (k *KineSpec) Type() string {
	if k == nil {
		return ""
	}

	url, err := url.Parse(k.DataSource)
	if err != nil {
		return ""
	}

	return url.Scheme
}

func (k *KineSpec) IsJoinable() bool {
	if k == nil {
		return false
	}

	switch k.Type() {
	case "mysql", "postgres":
		return true
	}

	return false
}

func (s *NetworkService) Address() string {
	if s == nil {
		return ""
	}

	if s.ExternalAddress != "" {
		return s.ExternalAddress
	}

	return s.BindAddress.IP.String()
}

func (s *NetworkService) URL() *url.URL {
	var host string
	if s.ExternalAddress != "" {
		if ip := net.ParseIP(s.ExternalAddress); ip != nil {
			host = (&NetAddress{ip, s.BindAddress.Port}).String()
		} else if s.BindAddress.Port != 0 {
			host = fmt.Sprintf("%s:%d", s.ExternalAddress, s.BindAddress.Port)
		} else {
			host = s.ExternalAddress
		}
	} else {
		host = s.BindAddress.String()
	}

	return &url.URL{Scheme: "https", Host: host}
}

func (a *NetAddress) String() string {
	if a == nil {
		return ""
	}

	var buf strings.Builder
	if a.IP != nil {
		if v4 := a.IP.To4(); v4 != nil {
			buf.WriteString(v4.String())
		} else /* it's an IPv6 */ {
			buf.WriteRune('[')
			buf.WriteString(a.IP.String())
			buf.WriteRune(']')
		}
	}
	if a.Port != 0 {
		buf.WriteRune(':')
		buf.WriteString(strconv.FormatUint(uint64(a.Port), 10))
	}

	return buf.String()
}

func (p IPPort) String() string {
	return strconv.FormatUint(uint64(p), 10)
}

func (p IPPort) Or(other uint16) IPPort {
	if p == 0 {
		return IPPort(other)
	}

	return p
}

func getNetworkAddress(ipNet *net.IPNet, converter func(net.IP) net.IP, ipLen int) (net.IP, int, error) {
	converted := converter(ipNet.IP)
	if len(converted) != ipLen {
		return nil, 0, fmt.Errorf("not a valid %d byte network address in %#v", ipLen, ipNet)
	}

	prefixLen, size := ipNet.Mask.Size()
	if size != 8*ipLen {
		return nil, 0, fmt.Errorf("not a valid %d byte network mask in %#v", ipLen, ipNet)
	}

	// Copy the address, so that the original network address remains unchanged.
	copied := make(net.IP, ipLen)
	copy(copied, converted)

	return copied, size - prefixLen, nil
}
