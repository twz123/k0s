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
	"net"
	"net/url"
	"testing"

	"github.com/k0sproject/k0s/pkg/config"
	"github.com/k0sproject/k0s/pkg/constant"

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
			ControlPlane: config.ControlPlaneSpec{
				Storage: config.StorageSpec{
					Type: config.EtcdStorageType,
					Etcd: &config.EtcdSpec{
						BindAddress: config.NetAddress{IP: net.IP{127, 0, 0, 1}, Port: 52379},
					},
				},
			},
		}

		result := make(map[string]string)
		a.Require().NoError(underTest.collectEtcdArgs(result))

		a.Equal(map[string]string{
			"etcd-servers":  "https://127.0.0.1:52379",
			"etcd-cafile":   "/var/lib/k0s/pki/etcd/ca.crt",
			"etcd-certfile": "/var/lib/k0s/pki/apiserver-etcd-client.crt",
			"etcd-keyfile":  "/var/lib/k0s/pki/apiserver-etcd-client.key",
		}, result)
	})

	a.T().Run("external etcd cluster with TLS", func(t *testing.T) {
		underTest := APIServer{
			K0sVars: k0sVars,
			ControlPlane: config.ControlPlaneSpec{
				Storage: config.StorageSpec{
					Type: config.ExternalEtcdStorageType,
					ExternalEtcd: &config.ExternalEtcdSpec{
						Endpoints: []url.URL{
							{Scheme: "https", Host: "192.168.10.10:2379"},
							{Scheme: "https", Host: "192.168.10.11:2379"},
						},
						EtcdPrefix: "k0s-tenant-1",
						TLS: &config.EtcdTLS{
							CertFile: "/etc/pki/tls/certs/etcd-client.crt",
							KeyFile:  "/etc/pki/tls/private/etcd-client.key",
							CAFile:   "/etc/pki/CA/ca.crt",
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
			ControlPlane: config.ControlPlaneSpec{
				Storage: config.StorageSpec{
					Type: config.ExternalEtcdStorageType,
					ExternalEtcd: &config.ExternalEtcdSpec{
						Endpoints: []url.URL{
							{Scheme: "http", Host: "192.168.10.10:2379"},
							{Scheme: "http", Host: "192.168.10.11:2379"},
						},
						EtcdPrefix: "k0s-tenant-1",
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
