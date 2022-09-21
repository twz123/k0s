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

package controller

import (
	"testing"

	"github.com/k0sproject/k0s/pkg/apis/k0s.k0sproject.io/v1beta1"
	"github.com/k0sproject/k0s/pkg/constant"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

type apiServerSuite struct {
	suite.Suite
}

func TestAPIServerSuite(t *testing.T) {
	apiServerSuite := &apiServerSuite{}

	suite.Run(t, apiServerSuite)
}

func (a *apiServerSuite) TestGetEtcdArgs() {
	k0sVars := constant.CfgVars{
		CertRootDir: "/var/lib/k0s/pki",
		EtcdCertDir: "/var/lib/k0s/pki/etcd",
	}

	a.T().Run("internal etcd cluster", func(t *testing.T) {
		underTest := APIServer{
			K0sVars: k0sVars,
			NodeSpec: v1beta1.ClusterSpec{
				Storage: &v1beta1.StorageSpec{
					Type: v1beta1.EtcdStorageType,
					Etcd: &v1beta1.EtcdConfig{},
				},
			},
		}

		result := make(map[string]string)
		a.Require().NoError(underTest.collectEtcdArgs(result))

		a.Equal(map[string]string{
			"etcd-servers":  "https://127.0.0.1:2379",
			"etcd-cafile":   "/var/lib/k0s/pki/etcd/ca.crt",
			"etcd-certfile": "/var/lib/k0s/pki/apiserver-etcd-client.crt",
			"etcd-keyfile":  "/var/lib/k0s/pki/apiserver-etcd-client.key",
		}, result)
	})

	a.T().Run("external etcd cluster with TLS", func(t *testing.T) {
		underTest := APIServer{
			K0sVars: k0sVars,
			NodeSpec: v1beta1.ClusterSpec{
				Storage: &v1beta1.StorageSpec{
					Type: v1beta1.EtcdStorageType,
					Etcd: &v1beta1.EtcdConfig{
						ExternalCluster: &v1beta1.ExternalCluster{
							Endpoints: []string{
								"https://192.168.10.10:2379",
								"https://192.168.10.11:2379",
							},
							EtcdPrefix:     "k0s-tenant-1",
							CaFile:         "/etc/pki/CA/ca.crt",
							ClientCertFile: "/etc/pki/tls/certs/etcd-client.crt",
							ClientKeyFile:  "/etc/pki/tls/private/etcd-client.key",
						},
					},
				},
			},
		}

		result := make(map[string]string)
		a.Require().NoError(underTest.collectEtcdArgs(result))

		a.Equal(map[string]string{
			"etcd-servers":  "https://192.168.10.10:2379,https://192.168.10.11:2379",
			"etcd-cafile":   "/etc/pki/CA/ca.crt",
			"etcd-certfile": "/etc/pki/tls/certs/etcd-client.crt",
			"etcd-keyfile":  "/etc/pki/tls/private/etcd-client.key",
			"etcd-prefix":   "k0s-tenant-1",
		}, result)
	})

	a.T().Run("external etcd cluster without TLS", func(t *testing.T) {
		underTest := APIServer{
			K0sVars: k0sVars,
			NodeSpec: v1beta1.ClusterSpec{
				Storage: &v1beta1.StorageSpec{
					Type: v1beta1.EtcdStorageType,
					Etcd: &v1beta1.EtcdConfig{
						ExternalCluster: &v1beta1.ExternalCluster{
							Endpoints: []string{
								"http://192.168.10.10:2379",
								"http://192.168.10.11:2379",
							},
							EtcdPrefix: "k0s-tenant-1",
						},
					},
				},
			},
		}

		result := make(map[string]string)
		a.Require().NoError(underTest.collectEtcdArgs(result))

		a.Equal(map[string]string{
			"etcd-servers": "http://192.168.10.10:2379,http://192.168.10.11:2379",
			"etcd-prefix":  "k0s-tenant-1",
		}, result)
	})
}

func TestGetServiceClusterIPRange(t *testing.T) {
	t.Run("single_stack_default", func(t *testing.T) {
		spec := v1beta1.ClusterSpec{
			API:     &v1beta1.APISpec{Address: "10.96.0.249"},
			Network: &v1beta1.Network{ServiceCIDR: "10.96.0.0/12"},
		}
		assert.Equal(t, "10.96.0.0/12", getServiceClusterIPRange(&spec))
	})
	t.Run("dual_stack_api_listens_on_ipv4", func(t *testing.T) {
		spec := v1beta1.ClusterSpec{
			API: &v1beta1.APISpec{Address: "10.96.0.249"},
			Network: &v1beta1.Network{
				ServiceCIDR: "10.96.0.0/12",
				DualStack: &v1beta1.DualStack{
					Enabled: true, IPv6ServiceCIDR: "fd00::/108",
				},
			},
		}
		assert.Equal(t, "10.96.0.0/12,fd00::/108", getServiceClusterIPRange(&spec))
	})
	t.Run("dual_stack_api_listens_on_ipv6", func(t *testing.T) {
		spec := v1beta1.ClusterSpec{
			API: &v1beta1.APISpec{Address: "fe80::cf8:3cff:fef2:c5ca"},
			Network: &v1beta1.Network{
				ServiceCIDR: "10.96.0.0/12",
				DualStack: &v1beta1.DualStack{
					Enabled: true, IPv6ServiceCIDR: "fd00::/108",
				},
			},
		}
		assert.Equal(t, "fd00::/108,10.96.0.0/12", getServiceClusterIPRange(&spec))
	})
}
