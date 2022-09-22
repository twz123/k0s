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

package nllb

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"golang.org/x/exp/slices"

	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	"github.com/avast/retry-go"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/multierr"
)

func TestKubeconfigPaths_Reconcile(t *testing.T) {

	// Represents a regular boostrap kubeconfig as written by k0s.
	bootstrap := clientcmdapi.Config{
		Clusters: map[string]*clientcmdapi.Cluster{"k0s": {
			Server:                   "https://10.70.123.158:6443/foobar",
			CertificateAuthorityData: []byte("the CA cert"),
		}},
		AuthInfos: map[string]*clientcmdapi.AuthInfo{"kubelet-bootstrap": {
			Token: "the token",
		}},
		Contexts: map[string]*clientcmdapi.Context{"k0s": {
			Cluster:  "k0s",
			AuthInfo: "kubelet-bootstrap",
		}},
		CurrentContext: "k0s",
	}

	// Represents a load balanced boostrap kubeconfig as expected for node-local load balancing.
	loadBalancedBootstrap := clientcmdapi.Config{
		Clusters: map[string]*clientcmdapi.Cluster{
			"k0s": {
				Server:                   "https://10.70.123.158:6443/foobar",
				CertificateAuthorityData: []byte("the CA cert"),
			},
			"k0s-node-local-load-balanced": {
				Server:                   "https://lb/foobar",
				CertificateAuthorityData: []byte("the CA cert"),
			},
		},
		AuthInfos: map[string]*clientcmdapi.AuthInfo{"kubelet-bootstrap": {
			Token: "the token",
		}},
		Contexts: map[string]*clientcmdapi.Context{
			"k0s": {
				Cluster:  "k0s",
				AuthInfo: "kubelet-bootstrap",
			},
			"k0s-node-local-load-balanced": {
				Cluster:  "k0s-node-local-load-balanced",
				AuthInfo: "kubelet-bootstrap",
			},
		},
		CurrentContext: "k0s-node-local-load-balanced",
	}

	// Represents a regular kubeconfig as written by kubelet after having used the bootstrap config.
	// https://github.com/kubernetes/kubernetes/blob/v1.25.0/pkg/kubelet/certificate/bootstrap/bootstrap.go#L185-L205
	kubeconfig := func(pathPrefix ...string) *clientcmdapi.Config {
		return &clientcmdapi.Config{
			Clusters: map[string]*clientcmdapi.Cluster{"default-cluster": {
				Server:                   "https://10.70.123.158:6443/foobar",
				CertificateAuthorityData: []byte("the CA cert"),
			}},
			AuthInfos: map[string]*clientcmdapi.AuthInfo{"default-auth": {
				ClientCertificate: filepath.Join(append(pathPrefix, "kubelet-client-current.pem")...),
				ClientKey:         filepath.Join(append(pathPrefix, "kubelet-client-current.pem")...),
			}},
			Contexts: map[string]*clientcmdapi.Context{"default-context": {
				Cluster:   "default-cluster",
				AuthInfo:  "default-auth",
				Namespace: "default",
			}},
			CurrentContext: "default-context",
		}
	}

	// Represents a regular kubeconfig as expected for node-local load balancing.
	loadBalanced := func(pathPrefix ...string) *clientcmdapi.Config {
		return &clientcmdapi.Config{
			Clusters: map[string]*clientcmdapi.Cluster{
				"default-cluster": {
					Server:                   "https://10.70.123.158:6443/foobar",
					CertificateAuthorityData: []byte("the CA cert"),
				},
				"k0s-node-local-load-balanced": {
					Server:                   "https://lb/foobar",
					CertificateAuthorityData: []byte("the CA cert"),
				},
			},
			AuthInfos: map[string]*clientcmdapi.AuthInfo{"default-auth": {
				ClientCertificate: filepath.Join(append(pathPrefix, "kubelet-client-current.pem")...),
				ClientKey:         filepath.Join(append(pathPrefix, "kubelet-client-current.pem")...),
			}},
			Contexts: map[string]*clientcmdapi.Context{
				"default-context": {
					Cluster:   "default-cluster",
					AuthInfo:  "default-auth",
					Namespace: "default",
				},
				"k0s-node-local-load-balanced": {
					Cluster:   "k0s-node-local-load-balanced",
					AuthInfo:  "default-auth",
					Namespace: "default",
				},
			},
			CurrentContext: "k0s-node-local-load-balanced",
		}
	}

	underTest := kubeconfigPaths{
		regular: kubeconfigPath{
			regular:      "regular-regular",
			loadBalanced: "regular-loadBalanced",
		},
		bootstrap: kubeconfigPath{
			regular:      "bootstrap-regular",
			loadBalanced: "bootstrap-loadBalanced",
		},
	}

	runRecoUntil := func(t *testing.T, lbAddr string, writer kubeconfigWriter, checkFn func() error) error {
		log := logrus.New()
		log.SetLevel(logrus.DebugLevel)
		log.Out = io.Discard
		log.Hooks.Add(&testLogHook{t, log})

		ctx, cancel := context.WithCancel(context.TODO())
		defer cancel()

		var recoErr error
		done := make(chan struct{})
		go func() {
			defer close(done)
			defer cancel()
			recoErr = underTest.reconcile(ctx, log, lbAddr, writer)
		}()

		awaitErr := retry.Do(
			func() error {
				err := checkFn()
				if err != nil && ctx.Err() != nil {
					return retry.Unrecoverable(fmt.Errorf("reconciliation terminated: %w", err))
				}
				return err
			},
			retry.LastErrorOnly(true),
			retry.MaxDelay(1*time.Second),
			retry.OnRetry(func(n uint, err error) {
				t.Logf("Awaiting reconciliation (%d): %v", n, err)
			}),
		)

		cancel()
		<-done
		return multierr.Append(awaitErr, recoErr)
	}

	for _, test := range []struct {
		name             string
		prepare          func(t *testing.T, pathPrefix ...string)
		expectedCalls    [][]string
		filesToBeTouched []kubeconfigPath
	}{
		{
			"Bootstrap_Regular",
			func(t *testing.T, pathPrefix ...string) {
				require.NoError(t, clientcmd.WriteToFile(bootstrap, underTest.bootstrap.regular))
			},
			[][]string{
				{"inSync", underTest.bootstrap.regular},
				{"update", underTest.bootstrap.loadBalanced},
				{"inSync", underTest.bootstrap.regular},
				{"inSync", underTest.bootstrap.loadBalanced},
			},
			[]kubeconfigPath{underTest.bootstrap},
		},
		{
			"Bootstrap_LoadBlanced",
			func(t *testing.T, pathPrefix ...string) {
				require.NoError(t, clientcmd.WriteToFile(loadBalancedBootstrap, underTest.bootstrap.loadBalanced))
			},
			[][]string{
				{"update", underTest.bootstrap.regular},
				{"inSync", underTest.bootstrap.loadBalanced},
				{"inSync", underTest.bootstrap.regular},
				{"inSync", underTest.bootstrap.loadBalanced},
			},
			[]kubeconfigPath{underTest.bootstrap},
		},
		{
			"Regular",
			func(t *testing.T, pathPrefix ...string) {
				require.NoError(t, clientcmd.WriteToFile(bootstrap, underTest.bootstrap.regular))
				require.NoError(t, clientcmd.WriteToFile(*kubeconfig(pathPrefix...), underTest.regular.regular))
			},
			[][]string{
				{"inSync", underTest.regular.regular},
				{"update", underTest.regular.loadBalanced},
				{"inSync", underTest.bootstrap.regular},
				{"update", underTest.bootstrap.loadBalanced},
				{"inSync", underTest.regular.regular},
				{"inSync", underTest.regular.loadBalanced},
				{"inSync", underTest.bootstrap.regular},
				{"inSync", underTest.bootstrap.loadBalanced},
			},
			[]kubeconfigPath{underTest.bootstrap, underTest.regular},
		},
		{
			"LoadBalanced",
			func(t *testing.T, pathPrefix ...string) {
				require.NoError(t, clientcmd.WriteToFile(loadBalancedBootstrap, underTest.bootstrap.loadBalanced))
				require.NoError(t, clientcmd.WriteToFile(*loadBalanced(pathPrefix...), underTest.regular.loadBalanced))
			},
			[][]string{
				{"update", underTest.regular.regular},
				{"inSync", underTest.regular.loadBalanced},
				{"update", underTest.bootstrap.regular},
				{"inSync", underTest.bootstrap.loadBalanced},
				{"inSync", underTest.regular.regular},
				{"inSync", underTest.regular.loadBalanced},
				{"inSync", underTest.bootstrap.regular},
				{"inSync", underTest.bootstrap.loadBalanced},
			},
			[]kubeconfigPath{underTest.bootstrap, underTest.regular},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			defer chdir(t, tmpDir)()

			test.prepare(t, tmpDir)

			writer := spyKubeconfigWriter{delegate: kubeconfigWriterOrDefault(nil)}
			assert.NoError(t, runRecoUntil(t, "lb", &writer, func() error {
				writer.mu.Lock()
				defer writer.mu.Unlock()

				for _, kubeconfigPath := range test.filesToBeTouched {
					for _, file := range []string{kubeconfigPath.regular, kubeconfigPath.loadBalanced} {
						if _, err := os.Stat(filepath.Join(file)); err != nil {
							return err
						}
					}
				}

				if len(test.expectedCalls) != len(writer.calls) {
					return fmt.Errorf("saw %d calls, expected %d calls", len(writer.calls), len(test.expectedCalls))
				}

				if !assert.Equal(t, test.expectedCalls, writer.calls) {
					return retry.Unrecoverable(errors.New("didn't observe expected calls"))
				}
				return nil
			}), "Reconciliation wasn't successful.")

			if slices.Contains(test.filesToBeTouched, underTest.bootstrap) {
				expected, err := clientcmd.Write(bootstrap)
				require.NoError(t, err)
				if actual, err := os.ReadFile(underTest.bootstrap.regular); assert.NoError(t, err) {
					assert.Equal(t, string(expected), string(actual), "Bootstrap kubeconfig didn't match expectations.")
				}

				expected, err = clientcmd.Write(loadBalancedBootstrap)
				require.NoError(t, err)
				if actual, err := os.ReadFile(underTest.bootstrap.loadBalanced); assert.NoError(t, err) {
					assert.Equal(t, string(expected), string(actual), "Load-balanced bootstrap kubeconfig didn't match expectations.")
				}
			}

			if slices.Contains(test.filesToBeTouched, underTest.regular) {
				// Since the kubeconfigs may reside in different folders, expect
				// that all relative paths have been resolved and replaced by
				// absolute ones.

				expected, err := clientcmd.Write(*kubeconfig(tmpDir))
				require.NoError(t, err)
				if actual, err := os.ReadFile(underTest.regular.regular); assert.NoError(t, err) {
					assert.Equal(t, string(expected), string(actual), "Kubeconfig didn't match expectations.")
				}

				expected, err = clientcmd.Write(*loadBalanced(tmpDir))
				require.NoError(t, err)
				if actual, err := os.ReadFile(underTest.regular.loadBalanced); assert.NoError(t, err) {
					assert.Equal(t, string(expected), string(actual), "Load-balanced kubeconfig didn't match expectations.")
				}
			}
		})
	}
}

// chdir changes the current working directory to the named directory and
// returns a function that, when called, restores the original working
// directory.
func chdir(t *testing.T, dir string) func() {
	wd, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	return func() {
		require.NoError(t, os.Chdir(wd))
	}
}

type testLogHook struct {
	*testing.T
	*logrus.Logger
}

func (t *testLogHook) Fire(e *logrus.Entry) error {
	bytes, err := t.Formatter.Format(e)
	if err != nil {
		return err
	}
	t.T.Log(string(bytes))
	return nil
}

func (*testLogHook) Levels() []logrus.Level {
	return logrus.AllLevels
}

type spyKubeconfigWriter struct {
	delegate kubeconfigWriter

	mu    sync.Mutex
	calls [][]string
}

func (m *spyKubeconfigWriter) update(path string, data []byte) error {
	err := m.delegate.update(path, data)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, []string{"update", path})
	return err
}

func (m *spyKubeconfigWriter) inSync(path string) {
	m.delegate.inSync(path)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, []string{"inSync", path})
}
