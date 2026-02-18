// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package helm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/k0sproject/k0s/pkg/kubernetes"
	"github.com/sirupsen/logrus"
	"helm.sh/helm/v3/pkg/kube"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/client-go/transport"
)

var errDisabled = errors.New("disabled")

// Allows bricking REST traffic for a specific action configuration.
type controlledRESTClientGetter struct {
	namespace  string
	clients    kubernetes.ClientFactory
	restMapper atomic.Pointer[restmapper.DeferredDiscoveryRESTMapper]
	disable    func()
}

func newControlledRESTClientGetter(namespace string, loadRESTConfig func() (*rest.Config, error)) *controlledRESTClientGetter {
	disabled := make(chan struct{})
	transportControl := &transportControl{disabled}

	return &controlledRESTClientGetter{
		namespace: namespace,
		clients: kubernetes.ClientFactory{LoadRESTConfig: func() (*rest.Config, error) {
			config, err := loadRESTConfig()
			if err != nil {
				return nil, err
			}
			return transportControl.controlledConfig(config), nil
		}},
		disable: sync.OnceFunc(func() { close(disabled) }),
	}
}

func (g *controlledRESTClientGetter) ToRESTConfig() (*rest.Config, error) {
	logrus.Infof("XOXO(%p) ToRESTConfig", g)
	return g.clients.GetRESTConfig()
}

func (g *controlledRESTClientGetter) ToDiscoveryClient() (discovery.CachedDiscoveryInterface, error) {
	logrus.Infof("XOXO(%p) ToDiscoveryClient", g)
	return g.clients.GetDiscoveryClient()
}

func (g *controlledRESTClientGetter) ToRESTMapper() (meta.RESTMapper, error) {
	logrus.Infof("XOXO(%p) ToRESTMapper", g)

	if m := g.restMapper.Load(); m != nil {
		return m, nil
	}

	discoveryClient, err := g.clients.GetDiscoveryClient()
	if err != nil {
		return nil, err
	}

	m := restmapper.NewDeferredDiscoveryRESTMapper(discoveryClient)
	if !g.restMapper.CompareAndSwap(nil, m) {
		m = g.restMapper.Load()
	}

	return m, nil
}

func (g *controlledRESTClientGetter) ToRawKubeConfigLoader() clientcmd.ClientConfig {
	return g
}

func (g *controlledRESTClientGetter) ClientConfig() (*rest.Config, error) {
	logrus.Infof("XOXO(%p) ClientConfig", g)
	return g.clients.GetRESTConfig()
}

// ConfigAccess implements [clientcmd.ClientConfig].
func (g *controlledRESTClientGetter) ConfigAccess() clientcmd.ConfigAccess {
	logrus.Infof("XOXO(%p) ConfigAccess", g)
	return &clientcmd.PathOptions{
		LoadingRules: &clientcmd.ClientConfigLoadingRules{},
	}
}

// Namespace implements [clientcmd.ClientConfig].
func (g *controlledRESTClientGetter) Namespace() (string, bool, error) {
	logrus.Infof("XOXO(%p) Namespace", g)
	return g.namespace, true, nil
}

// RawConfig implements [clientcmd.ClientConfig].
func (g *controlledRESTClientGetter) RawConfig() (api.Config, error) {
	return api.Config{}, fmt.Errorf("%w: RawConfig", errors.ErrUnsupported)
}

type transportControl struct {
	disabled <-chan struct{}
}

func (c *transportControl) controlledConfig(config *rest.Config) *rest.Config {
	wrapTransport := config.WrapTransport
	config = rest.CopyConfig(config)

	wrappers := make([]transport.WrapperFunc, 0, 4)
	wrappers = append(wrappers, c.transport)
	wrappers = append(wrappers, c.roundTripper)
	if wrapTransport != nil {
		wrappers = append(wrappers, wrapTransport)
	}
	wrappers = append(wrappers, func(rt http.RoundTripper) http.RoundTripper {
		return &kube.RetryingRoundTripper{Wrapped: rt}
	})

	config.WrapTransport = transport.Wrappers(wrappers...)

	return config
}

func (c *transportControl) transport(rt http.RoundTripper) http.RoundTripper {
	transport, ok := rt.(*http.Transport)
	if !ok {
		err := fmt.Errorf("expected an http.Transport, got %T", rt)
		return roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			return nil, err
		})
	}

	transport = transport.Clone()

	dial := transport.DialContext
	if dial == nil {
		dialer := &net.Dialer{}
		dial = dialer.DialContext
	}
	transport.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return c.dial(ctx, func(ctx context.Context) (net.Conn, error) {
			return dial(ctx, network, addr)
		})
	}

	if dial := transport.DialTLSContext; dial != nil {
		transport.DialTLSContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			return c.dial(ctx, func(ctx context.Context) (net.Conn, error) {
				return dial(ctx, network, addr)
			})
		}
	}

	return transport
}

func (c *transportControl) roundTripper(rt http.RoundTripper) http.RoundTripper {
	return roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		return c.roundTrip(rt, req)
	})
}

func (c *transportControl) roundTrip(rt http.RoundTripper, req *http.Request) (*http.Response, error) {
	select {
	case <-c.disabled:
		return nil, errDisabled
	default:
	}

	ctx, cancel := context.WithCancelCause(req.Context())
	go func() {
		select {
		case <-c.disabled:
			cancel(errDisabled)
		case <-ctx.Done():
		}
	}()

	resp, err := rt.RoundTrip(req.Clone(ctx))
	if err != nil {
		cancel(nil)
		return nil, err
	}

	body := resp.Body
	if body == nil {
		cancel(nil)
		return resp, nil
	}

	close := sync.OnceValue(func() error {
		err := body.Close()
		cancel(nil)
		return err
	})

	if rw, ok := body.(io.ReadWriter); ok {
		resp.Body = &closeWrappingReadWriter{rw, close}
	} else {
		resp.Body = &closeWrappingReader{body, close}
	}

	return resp, nil
}

func (c *transportControl) dial(ctx context.Context, dial func(context.Context) (net.Conn, error)) (net.Conn, error) {
	select {
	case <-c.disabled:
		return nil, errDisabled
	default:
	}

	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	go func() {
		select {
		case <-c.disabled:
			cancel(errDisabled)
		case <-ctx.Done():
		}
	}()

	conn, err := dial(ctx)
	if err != nil {
		return nil, err
	}

	closing := make(chan struct{})
	close := sync.OnceValue(func() error {
		close(closing)
		return conn.Close()
	})

	go func() {
		select {
		case <-c.disabled:
			_ = close()
		case <-closing:
		}
	}()

	return &closeWrappingConn{conn, close}, nil
}

type roundTripperFunc func(req *http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type closeWrappingConn struct {
	net.Conn
	close func() error
}

func (c *closeWrappingConn) Close() error { return c.close() }

type closeWrappingReader struct {
	io.Reader
	close func() error
}

func (r *closeWrappingReader) Close() error { return r.close() }

type closeWrappingReadWriter struct {
	io.ReadWriter
	close func() error
}

func (rw *closeWrappingReadWriter) Close() error { return rw.close() }
