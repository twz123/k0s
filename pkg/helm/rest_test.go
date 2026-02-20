// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package helm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/k0sproject/k0s/pkg/k0scontext"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestControlledRESTClientGetter_ImplementsClientConfig(t *testing.T) {
	underTest := newControlledRESTClientGetter("ns", func() (*rest.Config, error) {
		return &rest.Config{Host: "https://does-not-matter.example.com"}, nil
	})

	if cfg, err := underTest.ToRESTConfig(); assert.NoError(t, err) {
		assert.Equal(t, "https://does-not-matter.example.com", cfg.Host)
	}

	if disco, err := underTest.ToDiscoveryClient(); assert.NoError(t, err) {
		assert.NotNil(t, disco)
	}

	loader := underTest.ToRawKubeConfigLoader()
	require.NotNil(t, loader)

	if cfg, err := underTest.ClientConfig(); assert.NoError(t, err) {
		assert.Equal(t, "https://does-not-matter.example.com", cfg.Host)
	}

	if namespace, overridden, err := loader.Namespace(); assert.NoError(t, err) {
		assert.True(t, overridden)
		assert.Equal(t, "ns", namespace)
	}

	assert.NotPanics(t, func() { loader.ConfigAccess() })

	_, err := loader.RawConfig()
	assert.ErrorContains(t, err, "unsupported")
}

func TestControlledRESTClientGetter_ToRESTMapperCachesMapper(t *testing.T) {
	underTest := newControlledRESTClientGetter("ns", func() (*rest.Config, error) {
		return &rest.Config{Host: "https://does-not-matter.example.com"}, nil
	})

	m1, err := underTest.ToRESTMapper()
	require.NoError(t, err)
	m2, err := underTest.ToRESTMapper()
	require.NoError(t, err)
	assert.Same(t, m1, m2)
}

func TestControlledRESTClientGetter_DisableReturnsStructuredLockedResponse(t *testing.T) {
	underTest := newControlledRESTClientGetter("ns", func() (*rest.Config, error) {
		return &rest.Config{Host: "https://does-not-matter.example.com"}, nil
	})

	cfg, err := underTest.ToRESTConfig()
	require.NoError(t, err)

	clients, err := kubernetes.NewForConfig(cfg)
	require.NoError(t, err)

	underTest.disable()

	_, err = clients.CoreV1().Namespaces().List(t.Context(), metav1.ListOptions{})
	var apiErr apierrors.APIStatus
	if assert.ErrorAs(t, err, &apiErr) {
		status := apiErr.Status()
		assert.Equal(t, metav1.StatusFailure, status.Status)
		assert.EqualValues(t, http.StatusLocked, status.Code)
		assert.Equal(t, errHelmOperationInterrupted.Error(), status.Message)
	}
}

func TestControlledRESTClientGetter_DisableInterruptsInflightDials(t *testing.T) {
	underTest := newControlledRESTClientGetter("ns", func() (*rest.Config, error) {
		return &rest.Config{Host: "http://does-not-matter.example.com"}, nil
	})

	cfg, err := underTest.ToRESTConfig()
	require.NoError(t, err)
	require.Nil(t, cfg.Transport)
	dialStarted := make(chan struct{})
	dialCtxDoneCause := make(chan error, 1)
	cfg.Transport = &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			close(dialStarted)
			<-ctx.Done()
			dialCtxDoneCause <- context.Cause(ctx)
			return nil, ctx.Err()
		},
	}

	clients, err := kubernetes.NewForConfig(cfg)
	require.NoError(t, err)

	var listErr error
	listDone := make(chan struct{})
	go func() {
		_, listErr = clients.CoreV1().Namespaces().List(t.Context(), metav1.ListOptions{})
		close(listDone)
	}()

	<-dialStarted

	underTest.disable()
	assert.Equal(t, <-dialCtxDoneCause, errHelmOperationInterrupted)

	<-listDone

	//nolint:testifylint // We need this error to be of this exact type.
	// Helm 3.20.0 doesn't use errors.As, but an explicit type check.
	if assert.IsType(t, &apierrors.StatusError{}, listErr) {
		//nolint:errorlint // We need this error to be of this exact type.
		// Helm 3.20.0 doesn't use errors.As, but an explicit type check.
		listErr := listErr.(*apierrors.StatusError)
		assert.Equal(t, metav1.StatusFailure, listErr.ErrStatus.Status)
		assert.EqualValues(t, http.StatusLocked, listErr.ErrStatus.Code)
		assert.Equal(t, errHelmOperationInterrupted.Error(), listErr.ErrStatus.Message)
	}
}

func TestControlledRESTClientGetter_RejectsNonHTTPTransportInput(t *testing.T) {
	underTest := newControlledRESTClientGetter("ns", func() (*rest.Config, error) {
		return &rest.Config{Host: "https://does-not-matter.example.com"}, nil
	})

	cfg, err := underTest.ToRESTConfig()
	require.NoError(t, err)
	require.Nil(t, cfg.Transport)
	cfg.Transport = roundTripperFunc(func(*http.Request) (*http.Response, error) {
		panic("unreachable: not an *http.Transport")
	})

	clients, err := kubernetes.NewForConfig(cfg)
	require.NoError(t, err)

	result := clients.RESTClient().Get().Do(t.Context())
	assert.ErrorContains(t, result.Error(), "expected an *http.Transport")
}

func TestControlledRESTClientGetter_ResponseBodyClosePropagates(t *testing.T) {
	underTest := newControlledRESTClientGetter("ns", func() (*rest.Config, error) {
		return &rest.Config{Host: "http://does-not-matter.example.com"}, nil
	})

	cfg, err := underTest.ToRESTConfig()
	require.NoError(t, err)
	require.Nil(t, cfg.Transport)
	cfg.Transport = startHTTPPipeServer(t)

	clients, err := kubernetes.NewForConfig(cfg)
	require.NoError(t, err)

	responded := make(chan struct{})
	nextResponder := &httpResponder{handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(responded)
		assert.Equal(t, "/api/v1/namespaces/no/pods/matter/log", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		if _, err := io.WriteString(w, "foo"); !assert.NoError(t, err) {
			return
		}
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}

		<-r.Context().Done()
	})}

	stream, err := clients.CoreV1().Pods("no").GetLogs("matter", &corev1.PodLogOptions{}).Stream(k0scontext.WithValue(t.Context(), nextResponder))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, stream.Close()) })

	var buf strings.Builder
	_, err = io.CopyN(&buf, stream, 3)
	require.NoError(t, err)
	assert.Equal(t, "foo", buf.String())

	conn := nextResponder.conn.Load()
	require.NotNil(t, conn)
	assert.False(t, conn.isClosed())

	require.NoError(t, stream.Close())
	<-responded
	assert.True(t, conn.isClosed())
}

func TestControlledRESTClientGetter_DisableInterruptsLogsStream(t *testing.T) {
	underTest := newControlledRESTClientGetter("ns", func() (*rest.Config, error) {
		return &rest.Config{Host: "http://does-not-matter.example.com"}, nil
	})

	cfg, err := underTest.ToRESTConfig()
	require.NoError(t, err)
	require.Nil(t, cfg.Transport)
	cfg.Transport = startHTTPPipeServer(t)

	clients, err := kubernetes.NewForConfig(cfg)
	require.NoError(t, err)

	nextResponder := &httpResponder{handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v1/namespaces/no/pods/matter/log", r.URL.Path)
		w.WriteHeader(http.StatusOK)
		if _, err := io.WriteString(w, "foo"); !assert.NoError(t, err) {
			return
		}
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}

		<-r.Context().Done()
	})}

	stream, err := clients.CoreV1().Pods("no").GetLogs("matter", &corev1.PodLogOptions{}).Stream(k0scontext.WithValue(t.Context(), nextResponder))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, stream.Close()) })

	var buf [3]byte
	_, err = io.ReadFull(stream, buf[:])
	require.NoError(t, err)
	assert.Equal(t, [...]byte{'f', 'o', 'o'}, buf)

	underTest.disable()
	_, err = stream.Read(buf[:])
	assert.Error(t, err)

	conn := nextResponder.conn.Load()
	require.NotNil(t, conn)
	assert.True(t, conn.isClosed())
}

func TestControlledRESTClientGetter_DisableInterruptsWatchStream(t *testing.T) {
	underTest := newControlledRESTClientGetter("ns", func() (*rest.Config, error) {
		return &rest.Config{Host: "http://does-not-matter.example.com"}, nil
	})

	cfg, err := underTest.ToRESTConfig()
	require.NoError(t, err)
	require.Nil(t, cfg.Transport)
	cfg.Transport = startHTTPPipeServer(t)

	clients, err := kubernetes.NewForConfig(cfg)
	require.NoError(t, err)

	nextResponder := &httpResponder{handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !assert.Equal(t, "/api/v1/namespaces/ns/pods?watch=true", r.URL.String(), "Unexpected request URL") {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(map[string]any{
			"type": string(watch.Added),
			"object": corev1.Pod{
				TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Pod"},
				ObjectMeta: metav1.ObjectMeta{Name: "foo", Namespace: "ns"},
			},
		}); !assert.NoError(t, err) {
			return
		}
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}

		<-r.Context().Done()
	})}

	watcher, err := clients.CoreV1().Pods("ns").Watch(k0scontext.WithValue(t.Context(), nextResponder), metav1.ListOptions{})
	require.NoError(t, err)
	t.Cleanup(watcher.Stop)

	if event, ok := <-watcher.ResultChan(); assert.True(t, ok, "Watch closed before first event") {
		assert.Equal(t, watch.Added, event.Type)
	}

	conn := nextResponder.conn.Load()
	require.NotNil(t, conn)
	assert.False(t, conn.isClosed())

	underTest.disable()

	if event, ok := <-watcher.ResultChan(); assert.True(t, ok, "Watch closed without error event") {
		// The Kubernetes client converts all errors (i.e. errors that don't
		// stem from parsing an API server error response) into an
		// InternalServerError metav1.Status.
		if assert.Equal(t, watch.Error, event.Type) && assert.IsType(t, &metav1.Status{}, event.Object) {
			s := event.Object.(*metav1.Status)
			assert.Equal(t, metav1.StatusFailure, s.Status)
			assert.Equal(t, int32(http.StatusInternalServerError), s.Code)
			assert.Equal(t, metav1.StatusReasonInternalError, s.Reason)
			assert.Contains(t, s.Message, errHelmOperationInterrupted.Error())
			if assert.NotNil(t, s.Details) {
				if c := s.Details.Causes; assert.Len(t, c, 2) {
					expectedMsg := "unable to decode an event from the watch stream: " + errHelmOperationInterrupted.Error()
					assert.Equal(t, metav1.CauseTypeUnexpectedServerResponse, c[0].Type)
					assert.Contains(t, c[0].Message, expectedMsg)
					assert.Equal(t, "ClientWatchDecoding", string(c[1].Type))
					assert.Contains(t, c[1].Message, expectedMsg)
				}
			}
		}
	}

	if event, ok := <-watcher.ResultChan(); assert.False(t, ok, "Watch didn't close after error event") {
		assert.True(t, conn.isClosed())
	} else {
		assert.Failf(t, "Unexpected event", "%v", event)
	}

	assert.True(t, conn.isClosed())
}

func startHTTPPipeServer(t *testing.T) *http.Transport {
	server := http.Server{
		Addr: "pipe",
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			conn, ok := k0scontext.Value[net.Conn](r.Context()).(*serverConn)
			if assert.True(t, ok, "No server connection in request context") {
				conn.handler.ServeHTTP(w, r)
			} else {
				w.WriteHeader(http.StatusInternalServerError)
			}
		}),
		BaseContext: func(net.Listener) context.Context { return t.Context() },
		ConnContext: k0scontext.WithValue[net.Conn],
	}

	listener := pipeListner{
		queue: make(chan net.Conn),
		done:  make(chan struct{}),
	}

	serverDone := make(chan error, 1)
	go func() { serverDone <- server.Serve(&listener) }()
	t.Cleanup(func() { server.Close(); assert.ErrorIs(t, http.ErrServerClosed, <-serverDone) })

	return &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			responder := k0scontext.Value[*httpResponder](ctx)
			if !assert.NotNil(t, responder, "No HTTP responder in dial context") {
				return nil, errors.New("no HTTP responder in dial context")
			}

			client, server := net.Pipe()
			cc := &clientConn{Conn: client, closed: make(chan struct{})}
			sc := &serverConn{Conn: server, handler: responder.handler}
			responder.conn.Store(cc)

			select {
			case listener.queue <- sc:
			case <-ctx.Done():
				cause := context.Cause(ctx)
				assert.Failf(t, "Dial context done", "Cause: %v", cause)
				return nil, fmt.Errorf("dial context done: %w", cause)
			}

			return cc, nil
		},
	}
}

type serverConn struct {
	net.Conn
	handler http.Handler
}

type clientConn struct {
	net.Conn
	closed chan struct{}
}

func (c *clientConn) Close() error {
	select {
	case <-c.closed:
	default:
		close(c.closed)
	}
	if c.Conn != nil {
		return c.Conn.Close()
	}
	return nil
}

func (c *clientConn) isClosed() bool {
	select {
	case <-c.closed:
		return true
	default:
		return false
	}
}

type httpResponder struct {
	handler http.Handler
	conn    atomic.Pointer[clientConn]
}

type pipeListner struct {
	queue chan net.Conn
	done  chan struct{}
}

// Accept implements [net.Listener].
func (p *pipeListner) Accept() (net.Conn, error) {
	select {
	case conn, ok := <-p.queue:
		if !ok {
			<-p.done
			return nil, net.ErrClosed
		}
		return conn, nil

	case <-p.done:
		return nil, net.ErrClosed
	}
}

// Addr implements [net.Listener].
func (p *pipeListner) Addr() net.Addr {
	panic("unimplemented")
}

// Close implements [net.Listener].
func (p *pipeListner) Close() error {
	close(p.done)
	return nil
}
