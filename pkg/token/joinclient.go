// SPDX-FileCopyrightText: 2021 k0s authors
// SPDX-License-Identifier: Apache-2.0

package token

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/k0sproject/k0s/pkg/apis/k0s/v1beta1"
	"github.com/k0sproject/k0s/pkg/kubernetes"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd/api"
)

// JoinClient is the client we can use to call k0s join APIs
type JoinClient struct {
	joinAddress string
	restClient  *rest.RESTClient
}

// NewJoinClient creates a new join API client from the token, which needs to
// be a controller token.
func (t JoinToken) NewJoinClient() (*JoinClient, error) {
	restConfig, err := kubernetes.ClientConfig(func() (*api.Config, error) {
		return t.loadKubeconfig(ControllerTokenAuthName)
	})
	if err != nil {
		return nil, err
	}

	restConfig = dynamic.ConfigFor(restConfig)
	restClient, err := rest.UnversionedRESTClientFor(restConfig)
	if err != nil {
		return nil, err
	}

	return &JoinClient{
		joinAddress: restConfig.Host,
		restClient:  restClient,
	}, nil
}

func (j *JoinClient) Address() string {
	return j.joinAddress
}

// GetCA calls the CA sync API
func (j *JoinClient) GetCA(ctx context.Context) (v1beta1.CaResponse, error) {
	var caData v1beta1.CaResponse

	b, err := j.restClient.Get().AbsPath("v1beta1", "ca").Do(ctx).Raw()
	if err == nil {
		err = json.Unmarshal(b, &caData)
	}

	return caData, err
}

// JoinEtcd calls the etcd join API
func (j *JoinClient) JoinEtcd(ctx context.Context, etcdRequest v1beta1.EtcdRequest) (v1beta1.EtcdResponse, error) {
	var etcdResponse v1beta1.EtcdResponse

	buf := new(bytes.Buffer)
	if err := json.NewEncoder(buf).Encode(etcdRequest); err != nil {
		return etcdResponse, err
	}

	b, err := j.restClient.Post().AbsPath("v1beta1", "etcd", "members").Body(buf).Do(ctx).Raw()
	if err == nil {
		err = json.Unmarshal(b, &etcdResponse)
	}

	return etcdResponse, err
}
