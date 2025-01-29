/*
Copyright 2024 k0s authors

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

package join_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"testing"

	k0sv1beta1 "github.com/k0sproject/k0s/pkg/apis/k0s/v1beta1"
	"github.com/k0sproject/k0s/pkg/join"

	bootstraptokenv1 "k8s.io/kubernetes/cmd/kubeadm/app/apis/bootstraptoken/v1"

	"github.com/cloudflare/cfssl/csr"
	"github.com/cloudflare/cfssl/initca"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClient_GetCA(t *testing.T) {
	t.Parallel()

	joinURL, certData := startFakeJoinServer(t, func(res http.ResponseWriter, req *http.Request) {
		assert.Equal(t, "/some/sub/path/v1beta1/ca", req.RequestURI)
		assert.Equal(t, []string{"Bearer the-id.the-secret"}, req.Header["Authorization"])
		_, err := res.Write([]byte("{}"))
		assert.NoError(t, err)
	})

	joinURL.Path = "/some/sub/path"
	kubeconfig, err := join.GenerateKubeconfig(joinURL.String(), certData, t.Name(), &bootstraptokenv1.BootstrapTokenString{ID: "the-id", Secret: "the-secret"})
	require.NoError(t, err)
	tok, err := join.JoinEncode(bytes.NewReader(kubeconfig))
	require.NoError(t, err)

	underTest, err := join.ClientFromToken(tok)
	require.NoError(t, err)

	response, err := underTest.GetCA(context.TODO())
	assert.NoError(t, err)
	assert.Zero(t, response)
}

func TestClient_JoinEtcd(t *testing.T) {
	t.Parallel()

	joinURL, certData := startFakeJoinServer(t, func(res http.ResponseWriter, req *http.Request) {
		assert.Equal(t, "/some/sub/path/v1beta1/etcd/members", req.RequestURI)
		assert.Equal(t, []string{"Bearer the-id.the-secret"}, req.Header["Authorization"])

		if body, err := io.ReadAll(req.Body); assert.NoError(t, err) {
			var data map[string]string
			if assert.NoError(t, json.Unmarshal(body, &data)) {
				assert.Equal(t, map[string]string{
					"node":        "the-node",
					"peerAddress": "the-peer-address",
				}, data)
			}
		}

		_, err := res.Write([]byte("{}"))
		assert.NoError(t, err)
	})

	joinURL.Path = "/some/sub/path"
	kubeconfig, err := join.GenerateKubeconfig(joinURL.String(), certData, t.Name(), &bootstraptokenv1.BootstrapTokenString{ID: "the-id", Secret: "the-secret"})
	require.NoError(t, err)
	tok, err := join.JoinEncode(bytes.NewReader(kubeconfig))
	require.NoError(t, err)

	underTest, err := join.ClientFromToken(tok)
	require.NoError(t, err)

	response, err := underTest.JoinEtcd(context.TODO(), k0sv1beta1.EtcdRequest{
		Node:        "the-node",
		PeerAddress: "the-peer-address",
	})
	assert.NoError(t, err)
	assert.Zero(t, response)
}

func TestClient_Cancellation(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name          string
		funcUnderTest func(context.Context, *join.Client) error
	}{
		{"GetCA", func(ctx context.Context, c *join.Client) error {
			_, err := c.GetCA(ctx)
			return err
		}},
		{"JoinEtcd", func(ctx context.Context, c *join.Client) error {
			_, err := c.JoinEtcd(ctx, k0sv1beta1.EtcdRequest{})
			return err
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			clientContext, cancelClientContext := context.WithCancelCause(context.Background())
			joinURL, certData := startFakeJoinServer(t, func(_ http.ResponseWriter, req *http.Request) {
				cancelClientContext(assert.AnError) // cancel the client's context
				<-req.Context().Done()              // block forever
			})

			kubeconfig, err := join.GenerateKubeconfig(joinURL.String(), certData, "", &bootstraptokenv1.BootstrapTokenString{})
			require.NoError(t, err)
			tok, err := join.JoinEncode(bytes.NewReader(kubeconfig))
			require.NoError(t, err)

			underTest, err := join.ClientFromToken(tok)
			require.NoError(t, err)

			err = test.funcUnderTest(clientContext, underTest)
			assert.ErrorIs(t, err, context.Canceled, "Expected the call to be cancelled")
			assert.Same(t, context.Cause(clientContext), assert.AnError, "Didn't receive an HTTP request")
		})
	}
}

func startFakeJoinServer(t *testing.T, handler func(http.ResponseWriter, *http.Request)) (*url.URL, []byte) {
	requestCtx, cancelRequests := context.WithCancel(context.Background())
	var ok bool

	listener, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		require.NoError(t, err)
	}
	defer func() {
		if !ok {
			assert.NoError(t, listener.Close())
		}
	}()

	addr := listener.Addr().(*net.TCPAddr)
	certData, _, keyData, err := initca.New(&csr.CertificateRequest{
		KeyRequest: csr.NewKeyRequest(),
		CN:         fmt.Sprintf("localhost:%d", addr.Port),
		Hosts:      []string{addr.IP.String()},
	})
	if !assert.NoError(t, err) {
		assert.NoError(t, listener.Close())
		t.FailNow()
	}
	cert, err := tls.X509KeyPair(certData, keyData)
	require.NoError(t, err)

	server := &http.Server{
		Addr: addr.String(),
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{cert},
		},
		Handler:     http.HandlerFunc(handler),
		BaseContext: func(net.Listener) context.Context { return requestCtx },
	}
	serverError := make(chan error)
	ok = true
	go func() { defer close(serverError); serverError <- server.ServeTLS(listener, "", "") }()
	t.Cleanup(func() {
		cancelRequests()
		if !assert.NoError(t, server.Shutdown(context.Background()), "Couldn't shutdown HTTP server") {
			return
		}
		assert.ErrorIs(t, <-serverError, http.ErrServerClosed, "HTTP server terminated unexpectedly")
	})

	return &url.URL{Scheme: "https", Host: server.Addr}, certData
}
