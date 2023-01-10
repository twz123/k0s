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

package kuberouter

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/k0sproject/k0s/inttest/common"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/client-go/util/exec"

	"github.com/avast/retry-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type KubeRouterHairpinSuite struct {
	common.FootlooseSuite
}

func (s *KubeRouterHairpinSuite) TestK0sGetsUp() {
	s.PutFile(s.ControllerNode(0), "/tmp/k0s.yaml", k0sConfigWithHairpinning)
	s.Require().NoError(s.InitController(0, "--config=/tmp/k0s.yaml", "--disable-components=konnectivity-server,metrics-server"))
	s.MakeDir(s.ControllerNode(0), "/var/lib/k0s/manifests/test")
	s.PutFile(s.ControllerNode(0), "/var/lib/k0s/manifests/test/pod.yaml", podManifest)
	s.PutFile(s.ControllerNode(0), "/var/lib/k0s/manifests/test/service.yaml", serviceManifest)
	s.Require().NoError(s.RunWorkers())

	restConfig, err := s.GetKubeConfig("controller0")
	s.Require().NoError(err)

	kc, err := kubernetes.NewForConfig(restConfig)
	s.Require().NoError(err)

	err = s.WaitForNodeReady("worker0", kc)
	s.NoError(err)

	err = s.WaitForNodeReady("worker1", kc)
	s.NoError(err)

	s.T().Log("waiting to see kube-router pods ready")
	s.NoError(common.WaitForKubeRouterReady(s.Context(), kc), "kube-router did not start")

	s.T().Log("waiting to see hairpin pod ready")
	err = common.WaitForPod(s.Context(), kc, "hairpin-pod", "default")
	s.Require().NoError(err)

	s.T().Run("check hairpin mode", func(t *testing.T) {
		ctx := s.Context()

		for _, test := range []struct {
			dnsName string
			desc    string
		}{
			{
				"localhost",
				"pod can reach itself via loopback",
			},
			{
				"hairpin",
				"pod can reach itself via service name",
			},
		} {
			t.Run(test.desc, func(t *testing.T) {
				err := retry.Do(
					func() error {
						ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
						defer cancel()
						stdout, stderr, err := execCurl(ctx, t, restConfig, fmt.Sprintf("http://%s", test.dnsName))

						if err != nil {
							// retry all errors that don't indicate a curl failure
							var exitErr exec.ExitError
							if !errors.As(err, &exitErr) {
								return err
							}

							if stdout != "" {
								t.Log("Stdout:", stdout)
							}
							if stderr != "" {
								t.Log("Stderr:", stderr)
							}
							assert.Fail(t, err.Error())
							return nil
						}

						assert.Empty(t, stderr, "Something was written to stderr")
						assert.Contains(t, stdout, "Thank you for using nginx.")
						return nil
					},
					retry.Context(ctx),
					retry.LastErrorOnly(true),
					retry.OnRetry(func(attempt uint, err error) {
						t.Logf("Failed to invoke curl in attempt #%d, retrying after backoff: %v", attempt+1, err)
					}),
				)

				assert.NoError(t, err, "Failed to invoke curl")
			})
		}
	})
}

func execCurl(ctx context.Context, t *testing.T, restConfig *rest.Config, url string) (stdout, stderr string, _ error) {
	client, err := kubernetes.NewForConfig(restConfig)
	require.NoError(t, err)

	scheme := runtime.NewScheme()
	require.NoError(t, corev1.AddToScheme(scheme))
	parameterCodec := runtime.NewParameterCodec(scheme)

	req := client.CoreV1().RESTClient().Post().
		Resource("pods").SubResource("exec").
		Namespace("default").Name("hairpin-pod").
		VersionedParams(&corev1.PodExecOptions{
			Container: "curl",
			Command:   []string{"curl", "--connect-timeout", "5", "-sS", url},
			Stdout:    true,
			Stderr:    true,
		}, parameterCodec)

	exec, err := remotecommand.NewSPDYExecutor(restConfig, "POST", req.URL())
	require.NoError(t, err)

	var outBytes bytes.Buffer
	var errBytes bytes.Buffer

	err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdout: &outBytes,
		Stderr: &errBytes,
	})

	return outBytes.String(), errBytes.String(), err
}

func TestKubeRouterHairpinSuite(t *testing.T) {
	s := KubeRouterHairpinSuite{
		common.FootlooseSuite{
			ControllerCount: 1,
			WorkerCount:     2,
		},
	}
	suite.Run(t, &s)
}

const k0sConfigWithHairpinning = `
spec:
  network:
    provider: kuberouter
`

const podManifest = `
apiVersion: v1
kind: Pod
metadata:
  name: hairpin-pod
  namespace: default
  labels:
    app.kubernetes.io/name: hairpin
spec:
  containers:
  - name: nginx
    image: docker.io/library/nginx:1.23.1-alpine
    ports:
    - containerPort: 80
  - name: curl
    image: docker.io/curlimages/curl:7.84.0
    command: ["/bin/sh", "-c"]
    args: ["tail -f /dev/null"]
`

const serviceManifest = `
apiVersion: v1
kind: Service
metadata:
  name: hairpin
  namespace: default
spec:
  selector:
    app.kubernetes.io/name: hairpin
  ports:
  - protocol: TCP
    port: 80
    targetPort: 80
`
