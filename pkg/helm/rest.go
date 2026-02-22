// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package helm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/k0sproject/k0s/pkg/kubernetes"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	_ "k8s.io/cli-runtime/pkg/genericclioptions" // for godoc
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/clientcmd/api"
	"k8s.io/client-go/transport"

	"helm.sh/helm/v3/pkg/kube"
)

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

// ToRESTConfig implements [genericclioptions.RESTClientGetter].
func (g *controlledRESTClientGetter) ToRESTConfig() (*rest.Config, error) {
	return g.ClientConfig()
}

// ToDiscoveryClient implements [genericclioptions.RESTClientGetter].
func (g *controlledRESTClientGetter) ToDiscoveryClient() (discovery.CachedDiscoveryInterface, error) {
	return g.clients.GetDiscoveryClient()
}

// ToRESTMapper implements [genericclioptions.RESTClientGetter].
func (g *controlledRESTClientGetter) ToRESTMapper() (meta.RESTMapper, error) {
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

// ToRawKubeConfigLoader implements [genericclioptions.RESTClientGetter].
func (g *controlledRESTClientGetter) ToRawKubeConfigLoader() clientcmd.ClientConfig {
	return g
}

func (g *controlledRESTClientGetter) ClientConfig() (*rest.Config, error) {
	config, err := g.clients.GetRESTConfig()
	if err != nil {
		return nil, err
	}

	return rest.CopyConfig(config), nil
}

// ConfigAccess implements [clientcmd.ClientConfig].
func (g *controlledRESTClientGetter) ConfigAccess() clientcmd.ConfigAccess {
	return &clientcmd.PathOptions{
		LoadingRules: &clientcmd.ClientConfigLoadingRules{},
	}
}

// Namespace implements [clientcmd.ClientConfig].
func (g *controlledRESTClientGetter) Namespace() (string, bool, error) {
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
	config = rest.CopyConfig(config)

	wrappers := make([]transport.WrapperFunc, 0, 4)
	wrappers = append(wrappers, c.transport) // Needs to come first to wrap *http.Transport.
	if wrapTransport := config.WrapTransport; wrapTransport != nil {
		wrappers = append(wrappers, wrapTransport) // This is the original wrapper.
	}
	wrappers = append(wrappers, c.roundTripper) // Tinkers with request contexts.
	wrappers = append(wrappers, func(rt http.RoundTripper) http.RoundTripper {
		return &kube.RetryingRoundTripper{Wrapped: rt}
	})

	config.WrapTransport = transport.Wrappers(wrappers...)

	return config
}

func (c *transportControl) transport(rt http.RoundTripper) http.RoundTripper {
	transport, ok := rt.(*http.Transport)
	if !ok {
		err := fmt.Errorf("expected an *http.Transport, got %T", rt)
		return roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			return nil, err
		})
	}

	transport = transport.Clone()

	if dial := transport.DialContext; dial == nil && transport.Dial != nil {
		err := errors.New("cannot deal with the deprecated transport.Dial")
		return roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			return nil, err
		})
	} else {
		if dial == nil {
			dial = (&net.Dialer{}).DialContext
		}
		transport.DialContext = c.wrapDial(dial)
	}

	if dial := transport.DialTLSContext; dial == nil {
		if transport.DialTLS != nil {
			err := errors.New("cannot deal with the deprecated transport.DialTLS")
			return roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				return nil, err
			})
		}
	} else {
		transport.DialTLSContext = c.wrapDial(dial)
	}

	return transport
}

type dialFunc = func(ctx context.Context, net, addr string) (net.Conn, error)

func (c *transportControl) wrapDial(dial dialFunc) dialFunc {
	return func(ctx context.Context, net, addr string) (net.Conn, error) {
		select {
		case <-c.disabled:
			return nil, errHelmOperationInterrupted
		default:
		}

		ctx, cancel := context.WithCancelCause(ctx)
		defer cancel(nil)

		go func() {
			select {
			case <-c.disabled:
				cancel(errHelmOperationInterrupted)
			case <-ctx.Done():
			}
		}()

		conn, err := dial(ctx, net, addr)
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
}

func (c *transportControl) roundTripper(rt http.RoundTripper) http.RoundTripper {
	return roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		return c.roundTrip(rt, req)
	})
}

func (c *transportControl) roundTrip(rt http.RoundTripper, req *http.Request) (*http.Response, error) {
	select {
	case <-c.disabled:
		return interruptedResponse(req), nil
	default:
	}

	ctx, cancel := context.WithCancelCause(req.Context())
	go func() {
		select {
		case <-c.disabled:
			cancel(errHelmOperationInterrupted)
		case <-ctx.Done():
		}
	}()

	resp, err := rt.RoundTrip(req.Clone(ctx))
	if err != nil {
		cancel(err)
		select {
		case <-c.disabled:
			// We've been interrupted. The only way to prevent Helm retries is
			// to fake an HTTP error response, as it's the only way to get an
			// unwrapped *apierrors.StatusErr returned by the Kubernetes client
			// libraries. This might change as soon as the respective Helm code
			// paths use errors.As to properly unwrap the errors.
			return interruptedResponse(req), nil
		default:
		}

		return nil, err
	}

	resp.Body = c.wrapBody(resp.Body, cancel)

	return resp, nil
}

var errHTTPBodyClosed = errors.New("HTTP body closed")

func (c *transportControl) wrapBody(body io.Reader, cancel context.CancelCauseFunc) io.ReadCloser {
	var close func() error
	if c, ok := body.(io.Closer); ok {
		close = sync.OnceValue(func() error {
			err := c.Close()
			cancel(errHTTPBodyClosed)
			return err
		})
	} else {
		close = func() error {
			cancel(errHTTPBodyClosed)
			return nil
		}
	}

	switch body := body.(type) {
	case flushableWritableBody:
		return &flushableWritableBodyWrapper{
			writableBodyWrapper[flushableWritableBody]{
				bodyWrapper[flushableWritableBody]{
					body, c.disabled, close,
				},
			},
		}

	case io.ReadWriter:
		return &writableBodyWrapper[io.ReadWriter]{
			bodyWrapper[io.ReadWriter]{
				body, c.disabled, close,
			},
		}

	case flushableBody:
		return &flushableBodyWrapper{
			bodyWrapper[flushableBody]{
				body, c.disabled, close,
			},
		}

	case nil:
		return &bodyWrapper[io.Reader]{bytes.NewReader(nil), c.disabled, close}

	default:
		return &bodyWrapper[io.Reader]{body, c.disabled, close}
	}
}

var errHelmOperationInterrupted error = helmOperationInterruptedErr{}

type helmOperationInterruptedErr struct{}

// Ensures that [errors.As] will unwrap this.
var _ apierrors.APIStatus = helmOperationInterruptedErr{}

// Error implements [error].
func (helmOperationInterruptedErr) Error() string {
	return "helm operation interrupted"
}

// Status implements [apierrors.APIStatus].
func (helmOperationInterruptedErr) Status() metav1.Status {
	return interruptedStatus()
}

func (h helmOperationInterruptedErr) As(target any) bool {
	switch e := target.(type) {
	case **apierrors.StatusError:
		*e = &apierrors.StatusError{ErrStatus: h.Status()}
		return true
	default:
		return false
	}
}

func interruptedStatus() metav1.Status {
	return metav1.Status{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Status",
			APIVersion: "v1",
		},
		Status:  metav1.StatusFailure,
		Code:    http.StatusLocked,
		Reason:  metav1.StatusReasonUnknown,
		Message: errHelmOperationInterrupted.Error(),
	}
}

func interruptedResponse(req *http.Request) *http.Response {
	status := interruptedStatus()
	body, _ := json.Marshal(&status)
	return &http.Response{
		Status:        http.StatusText(http.StatusLocked) + ": " + status.Message,
		StatusCode:    http.StatusLocked,
		Proto:         req.Proto,
		ProtoMajor:    req.ProtoMajor,
		ProtoMinor:    req.ProtoMinor,
		Header:        http.Header{"Content-Type": []string{"application/json"}},
		ContentLength: int64(len(body)),
		Body:          io.NopCloser(bytes.NewReader(body)),
		Close:         true,
		Request:       req,
		TLS:           req.TLS,
	}
}

type roundTripperFunc func(req *http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

type closeWrappingConn struct {
	net.Conn
	close func() error
}

func (c *closeWrappingConn) Close() error { return c.close() }

type flushableBody interface {
	io.Reader
	http.Flusher
}

type flushableWritableBody interface {
	io.ReadWriter
	http.Flusher
}

type bodyWrapper[T io.Reader] struct {
	inner    T
	disabled <-chan struct{}
	close    func() error
}

// Read implements [io.ReadCloser].
func (w *bodyWrapper[T]) Read(p []byte) (int, error) {
	n, err := w.inner.Read(p)
	return n, w.wrapErr(err)
}

// Close implements [io.ReadCloser].
func (w *bodyWrapper[T]) Close() error {
	return w.close()
}

func (w *bodyWrapper[T]) wrapErr(err error) error {
	if err != nil {
		select {
		case <-w.disabled:
			if !errors.Is(err, errHelmOperationInterrupted) {
				return fmt.Errorf("%w (%w)", errHelmOperationInterrupted, err)
			}
		default:
		}
	}

	return err
}

type flushableBodyWrapper struct {
	bodyWrapper[flushableBody]
}

// Flush implements [http.Flusher].
func (w *flushableBodyWrapper) Flush() {
	w.inner.Flush()
}

type writableBodyWrapper[T io.ReadWriter] struct {
	bodyWrapper[T]
}

// Write implements [io.ReadWriteCloser].
func (w *writableBodyWrapper[T]) Write(p []byte) (int, error) {
	n, err := w.inner.Write(p)
	return n, w.wrapErr(err)
}

type flushableWritableBodyWrapper struct {
	writableBodyWrapper[flushableWritableBody]
}

// Flush implements [http.Flusher].
func (w *flushableWritableBodyWrapper) Flush() {
	w.inner.Flush()
}
