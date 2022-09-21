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

package etcd

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"

	"github.com/k0sproject/k0s/pkg/config"
	"github.com/k0sproject/k0s/pkg/constant"

	"go.etcd.io/etcd/api/v3/v3rpc/rpctypes"
	"go.etcd.io/etcd/client/pkg/v3/transport"
	client "go.etcd.io/etcd/client/v3"
)

const (
	CertFile  = "apiserver-etcd-client.crt"
	KeyFile   = "apiserver-etcd-client.key"
	CAFile    = "ca.crt"
	CAKeyFile = "ca.key"
)

// Client is our internal helper to access some of the etcd APIs
type Client struct {
	client *client.Client
}

type ConfigGetter func() (client.Config, error)

func (g ConfigGetter) Get() (client.Config, error) {
	return g()
}

func (g ConfigGetter) NewClient() (*Client, error) {
	config, err := g.Get()
	if err != nil {
		return nil, err
	}

	client, err := client.New(config)
	if err != nil {
		return nil, err
	}

	return &Client{client}, nil
}

func ConfigFromEndpoints(endpoints []url.URL, etls *config.EtcdTLS) ConfigGetter {
	eps := make([]string, len(endpoints))
	for i, endpoint := range endpoints {
		eps[i] = endpoint.String()
	}

	if etls == nil {
		return func() (client.Config, error) {
			return client.Config{Endpoints: eps}, nil
		}
	}

	tlsInfo := transport.TLSInfo{
		CertFile:      etls.CertFile,
		KeyFile:       etls.KeyFile,
		TrustedCAFile: etls.CAFile,
	}

	return func() (config client.Config, err error) {
		config.Endpoints = eps
		config.TLS, err = tlsInfo.ClientConfig()
		return
	}
}

type UnsupportedStorageType string

func IsUnsupportedStorageType(err error) bool {
	var unsupported UnsupportedStorageType
	return errors.As(err, &unsupported)
}

func (t UnsupportedStorageType) Error() string {
	return fmt.Sprintf("cannot create etcd client for storage type %q", string(t))
}

func ConfigFromSpec(k0sVars *constant.CfgVars, spec *config.StorageSpec) ConfigGetter {
	switch spec.Type {
	case "Etcd":
		return ConfigFromEndpoints([]url.URL{*spec.Etcd.URL()}, &config.EtcdTLS{
			CertFile: filepath.Join(k0sVars.CertRootDir, CertFile),
			KeyFile:  filepath.Join(k0sVars.CertRootDir, KeyFile),
			CAFile:   filepath.Join(k0sVars.EtcdCertDir, CAFile),
		})

	case "ExternalEtcd":
		return ConfigFromEndpoints(spec.ExternalEtcd.Endpoints, spec.ExternalEtcd.TLS)

	default:
		err := UnsupportedStorageType(spec.Type)
		return func() (config client.Config, _ error) {
			return config, err
		}
	}
}

// ListMembers gets a list of current etcd members
func (c *Client) ListMembers(ctx context.Context) (map[string]string, error) {
	memberList := make(map[string]string)
	members, err := c.client.MemberList(ctx)
	if err != nil {
		return nil, err
	}
	for _, m := range members.Members {
		memberList[m.Name] = m.PeerURLs[0]
	}

	return memberList, nil
}

// AddMember add new member to etcd cluster
func (c *Client) AddMember(ctx context.Context, name, peerAddress string) ([]string, error) {

	addResp, err := c.client.MemberAdd(ctx, []string{peerAddress})
	if err != nil {
		// TODO we should try to detect possible double add for a peer
		// Not sure though if we can return correct initial-cluster as the order
		// is important for the peers :/
		return nil, err
	}

	newID := addResp.Member.ID

	var memberList []string
	for _, m := range addResp.Members {
		memberName := m.Name
		if m.ID == newID {
			memberName = name
		}
		memberList = append(memberList, fmt.Sprintf("%s=%s", memberName, m.PeerURLs[0]))
	}

	return memberList, nil
}

// GetPeerIDByAddress looks up peer id by peer url
func (c *Client) GetPeerIDByAddress(ctx context.Context, peerAddress string) (uint64, error) {
	resp, err := c.client.MemberList(ctx)
	if err != nil {
		return 0, fmt.Errorf("etcd member list failed: %w", err)
	}
	for _, m := range resp.Members {
		for _, peerURL := range m.PeerURLs {
			if peerURL == peerAddress {
				return m.ID, nil
			}
		}
	}
	return 0, fmt.Errorf("peer not found: %s", peerAddress)
}

// DeleteMember deletes member by peer name
func (c *Client) DeleteMember(ctx context.Context, peerID uint64) error {
	_, err := c.client.MemberRemove(ctx, peerID)
	return err
}

// Close closes the etcd client
func (c *Client) Close() {
	c.client.Close()
}

// Health return err if the etcd peer is not reported as healthy
// ref: https://github.com/etcd-io/etcd/blob/3ead91ca3edf66112d56c453169343515bba71c3/etcdctl/ctlv3/command/ep_command.go#L89
func (c *Client) Health(ctx context.Context) error {
	_, err := c.client.Get(ctx, "health")

	// permission denied is OK since proposal goes through consensus to get it
	if err == nil || err == rpctypes.ErrPermissionDenied {
		return nil
	}

	return err

}
