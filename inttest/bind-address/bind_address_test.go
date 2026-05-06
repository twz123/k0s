// SPDX-FileCopyrightText: 2024 k0s authors
// SPDX-License-Identifier: Apache-2.0

package bind_address

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"testing"
	"time"

	"github.com/k0sproject/k0s/inttest/common"
	"github.com/k0sproject/k0s/pkg/apis/k0s/v1beta1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	kubeletv1beta1 "k8s.io/kubelet/config/v1beta1"

	testifysuite "github.com/stretchr/testify/suite"
	"golang.org/x/sync/errgroup"
	"sigs.k8s.io/yaml"
)

type suite struct {
	common.BootlooseSuite
}

func (s *suite) TestCustomizedBindAddress() {
	const controllerArgs = "--kube-controller-manager-extra-args='--node-monitor-period=3s --node-monitor-grace-period=9s'"

	ctx := s.Context()

	{
		for i := range s.ControllerCount {
			config, err := bindAddressConfig(s.GetIPAddress(s.ControllerNode(i)))
			s.Require().NoError(err)
			s.WriteFileContent(s.ControllerNode(i), "/tmp/k0s.yaml", config)
		}
	}

	s.Run("controller_and_workers_get_up", func() {
		s.Require().NoError(s.InitController(0, "--config=/tmp/k0s.yaml", controllerArgs))

		s.T().Logf("Starting workers and waiting for cluster to become ready")

		token, err := s.GetJoinToken("worker")
		s.Require().NoError(err)
		s.Require().NoError(s.RunWorkersWithToken(token))

		clients, err := s.KubeClient(s.ControllerNode(0))
		s.Require().NoError(err)

		eg, _ := errgroup.WithContext(ctx)
		for i := range s.WorkerCount {
			nodeName := s.WorkerNode(i)
			eg.Go(func() error {
				if err := s.WaitForNodeReady(nodeName, clients); err != nil {
					return fmt.Errorf("Node %s is not ready: %w", nodeName, err)
				}
				return nil
			})
		}
		s.Require().NoError(eg.Wait())

		s.Require().NoError(s.checkClusterReadiness(ctx, clients))
	})

	s.Run("join_new_controllers", func() {
		token, err := s.GetJoinToken("controller")
		s.Require().NoError(err)

		eg, _ := errgroup.WithContext(ctx)
		eg.Go(func() error { return s.InitController(1, "--config=/tmp/k0s.yaml", controllerArgs, token) })
		eg.Go(func() error { return s.InitController(2, "--config=/tmp/k0s.yaml", controllerArgs, token) })

		s.Require().NoError(eg.Wait())

		clients, err := s.KubeClient(s.ControllerNode(1))
		s.Require().NoError(err)

		s.T().Logf("Checking if HA cluster is ready")
		s.Require().NoError(s.checkClusterReadiness(ctx, clients))
	})
}

type staleLocalhostSuite struct {
	common.BootlooseSuite
}

func (s *staleLocalhostSuite) TestControllerWorkerKubeletConfigIsRewritten() {
	node := s.ControllerNode(0)
	config, err := bindAddressConfig(s.GetIPAddress(node))
	s.Require().NoError(err)
	s.WriteFileContent(node, "/tmp/k0s.yaml", config)

	s.Require().NoError(s.InitController(0, "--config=/tmp/k0s.yaml", "--enable-worker"))

	clients, err := s.KubeClient(node)
	s.Require().NoError(err)
	s.Require().NoError(s.WaitForNodeReady(node, clients))

	s.Require().NoError(s.StopController(node))
	s.patchKubeletConfigServer(node, "https://localhost:6443")
	s.Require().NoError(s.StartController(node))

	expectedServer := (&url.URL{Scheme: "https", Host: net.JoinHostPort(s.GetIPAddress(node), "6443")}).String()
	s.Require().Eventually(func() bool {
		server, err := s.kubeletConfigServer(node)
		return err == nil && server == expectedServer
	}, time.Minute, time.Second)

	s.Require().Eventually(func() bool {
		return s.checkStatusKubeAPIProbe(node) == nil
	}, 2*time.Minute, 5*time.Second)
}

func (s *staleLocalhostSuite) patchKubeletConfigServer(node, server string) {
	ssh, err := s.SSH(s.Context(), node)
	s.Require().NoError(err)
	defer ssh.Disconnect()

	s.Require().NoError(ssh.Exec(s.Context(), fmt.Sprintf("sed -i 's#server: .*#server: %s#' /var/lib/k0s/kubelet.conf", server), common.SSHStreams{}))
}

func (s *staleLocalhostSuite) kubeletConfigServer(node string) (string, error) {
	ssh, err := s.SSH(s.Context(), node)
	if err != nil {
		return "", err
	}
	defer ssh.Disconnect()

	return ssh.ExecWithOutput(s.Context(), "awk '$1 == \"server:\" { print $2 }' /var/lib/k0s/kubelet.conf")
}

func (s *staleLocalhostSuite) checkStatusKubeAPIProbe(node string) error {
	ssh, err := s.SSH(s.Context(), node)
	if err != nil {
		return err
	}
	defer ssh.Disconnect()

	output, err := ssh.ExecWithOutput(s.Context(), "k0s status -ojson")
	if err != nil {
		return err
	}

	var status map[string]any
	if err := json.Unmarshal([]byte(output), &status); err != nil {
		return err
	}

	success, found, err := unstructured.NestedBool(status, "WorkerToAPIConnectionStatus", "Success")
	if err != nil {
		return err
	}
	if !found || !success {
		return fmt.Errorf("kube-api status probe did not succeed: %s", output)
	}
	return nil
}

func bindAddressConfig(address string) ([]byte, error) {
	kubeletConfig := kubeletv1beta1.KubeletConfiguration{
		NodeStatusUpdateFrequency: metav1.Duration{Duration: 3 * time.Second},
	}
	kubeletConfigBytes, err := json.Marshal(kubeletConfig)
	if err != nil {
		return nil, err
	}

	return yaml.Marshal(&v1beta1.ClusterConfig{
		Spec: &v1beta1.ClusterSpec{
			API: func() *v1beta1.APISpec {
				apiSpec := v1beta1.DefaultAPISpec()
				apiSpec.Address = address
				apiSpec.OnlyBindToAddress = true
				return apiSpec
			}(),
			WorkerProfiles: v1beta1.WorkerProfiles{
				v1beta1.WorkerProfile{
					Name:   "default",
					Config: &runtime.RawExtension{Raw: kubeletConfigBytes},
				},
			},
		},
	})
}

func (s *suite) checkClusterReadiness(ctx context.Context, clients *kubernetes.Clientset) error {
	eg, ctx := errgroup.WithContext(ctx)

	eg.Go(func() error {
		if err := common.WaitForKubeRouterReady(ctx, clients); err != nil {
			return fmt.Errorf("kube-router did not start: %w", err)
		}
		s.T().Logf("kube-router is ready")
		return nil
	})

	for _, lease := range []string{"kube-scheduler", "kube-controller-manager"} {
		eg.Go(func() error {
			id, err := common.WaitForLease(ctx, clients, lease, metav1.NamespaceSystem)
			if err != nil {
				return fmt.Errorf("%s has no leader: %w", lease, err)
			}
			s.T().Logf("%s has a leader: %q", lease, id)
			return nil
		})
	}

	for _, daemonSet := range []string{"kube-proxy", "konnectivity-agent"} {
		eg.Go(func() error {
			if err := common.WaitForDaemonSet(ctx, clients, daemonSet, metav1.NamespaceSystem); err != nil {
				return fmt.Errorf("%s is not ready: %w", daemonSet, err)
			}
			s.T().Log(daemonSet, "is ready")
			return nil
		})
	}

	for _, deployment := range []string{"coredns", "metrics-server"} {
		eg.Go(func() error {
			if err := common.WaitForDeployment(ctx, clients, deployment, metav1.NamespaceSystem); err != nil {
				return fmt.Errorf("%s did not become ready: %w", deployment, err)
			}
			s.T().Log(deployment, "is ready")
			return nil
		})
	}

	return eg.Wait()
}

func TestCustomizedBindAddressSuite(t *testing.T) {
	s := suite{
		common.BootlooseSuite{
			ControllerCount: 3,
			WorkerCount:     1,
		},
	}
	testifysuite.Run(t, &s)
}

func TestStaleLocalhostSuite(t *testing.T) {
	s := staleLocalhostSuite{
		common.BootlooseSuite{
			ControllerCount: 1,
		},
	}
	testifysuite.Run(t, &s)
}
