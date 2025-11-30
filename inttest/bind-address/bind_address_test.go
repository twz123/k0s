// SPDX-FileCopyrightText: 2024 k0s authors
// SPDX-License-Identifier: Apache-2.0

package bind_address

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/k0sproject/k0s/internal/testutil"
	k0sv1beta1 "github.com/k0sproject/k0s/pkg/apis/k0s/v1beta1"
	kubeutil "github.com/k0sproject/k0s/pkg/kubernetes"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	kubeletv1beta1 "k8s.io/kubelet/config/v1beta1"

	"github.com/k0sproject/k0s/inttest/common"
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
			config, err := yaml.Marshal(&k0sv1beta1.ClusterConfig{
				Spec: &k0sv1beta1.ClusterSpec{
					API: func() *k0sv1beta1.APISpec {
						apiSpec := k0sv1beta1.DefaultAPISpec()
						apiSpec.Address = s.GetIPAddress(s.ControllerNode(i))
						apiSpec.OnlyBindToAddress = true
						apiSpec.ExternalAddress = s.GetLBAddress()
						return apiSpec
					}(),
					WorkerProfiles: k0sv1beta1.WorkerProfiles{
						k0sv1beta1.WorkerProfile{
							Name: "default",
							Config: func() *runtime.RawExtension {
								kubeletConfig := kubeletv1beta1.KubeletConfiguration{
									NodeStatusUpdateFrequency: metav1.Duration{Duration: 3 * time.Second},
								}
								bytes, err := json.Marshal(kubeletConfig)
								s.Require().NoError(err)
								return &runtime.RawExtension{Raw: bytes}
							}(),
						},
					},
				},
			})
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

		clientFactory := s.ClientFactory(s.ControllerNode(0))
		clients, err := clientFactory.GetClient()
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

		s.Require().NoError(s.checkClusterReadiness(ctx, 1, clientFactory))
	})

	s.Run("join_new_controllers", func() {
		token, err := s.GetJoinToken("controller")
		s.Require().NoError(err)

		eg, _ := errgroup.WithContext(ctx)
		eg.Go(func() error { return s.InitController(1, "--config=/tmp/k0s.yaml", controllerArgs, token) })
		eg.Go(func() error { return s.InitController(2, "--config=/tmp/k0s.yaml", controllerArgs, token) })

		s.Require().NoError(eg.Wait())

		s.T().Logf("Checking if HA cluster is ready")
		s.Require().NoError(s.checkClusterReadiness(ctx, 3, s.ClientFactory(s.ControllerNode(1))))
	})
}

func (s *suite) checkClusterReadiness(ctx context.Context, numControllers uint, clientFactory kubeutil.ClientFactoryInterface) error {
	clients, err := clientFactory.GetClient()
	if err != nil {
		return err
	}

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

	eg.Go(func() error {
		if err := common.WaitForDaemonSet(ctx, clients, "kube-proxy", metav1.NamespaceSystem); err != nil {
			return fmt.Errorf("kube-proxy is not ready: %w", err)
		}
		s.T().Log("kube-proxy is ready")
		return nil
	})

	eg.Go(func() (err error) {
		if err := common.WaitForDaemonSet(ctx, clients, "konnectivity-agent", metav1.NamespaceSystem); err != nil {
			return fmt.Errorf("konnectivity-agent is not ready: %w", err)
		}
		s.T().Log("konnectivity-agent is ready")

		pods, err := clients.CoreV1().Pods(metav1.NamespaceSystem).List(ctx, metav1.ListOptions{
			LabelSelector: fields.OneTermEqualSelector("k8s-app", "konnectivity-agent").String(),
		})
		if err != nil {
			return fmt.Errorf("failed to get konnectivity-agent pod: %w", err)
		}
		if len(pods.Items) < 1 {
			return errors.New("no konnectivity-agent pods found")
		}

		restConfig, err := clientFactory.GetRESTConfig()
		if err != nil {
			return err
		}
		dialer, err := testutil.NewPodDialer(restConfig)
		if err != nil {
			return err
		}
		transport := &http.Transport{DialContext: dialer.DialContext}
		defer transport.CloseIdleConnections()
		c := http.Client{Transport: transport}

		var lastErr error
		err = wait.PollUntilContextCancel(ctx, 1*time.Second, true, func(ctx context.Context) (done bool, err error) {
			if req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+pods.Items[0].Name+"."+metav1.NamespaceSystem+":8093/metrics", nil); err != nil {
				return false, fmt.Errorf("failed to create HTTP request: %w", err)
			} else if resp, err := c.Do(req); err != nil {
				lastErr = fmt.Errorf("failed to send HTTP request: %w", err)
				return false, nil
			} else {
				defer func() {
					if closeErr := resp.Body.Close(); err != nil {
						err = errors.Join(err, fmt.Errorf("failed to close HTTP response body: %w", closeErr))
					}
				}()

				if resp.StatusCode != http.StatusOK {
					lastErr = fmt.Errorf("failed to fetch metrics: %s", resp.Status)
					return false, nil
				}

				var openConns uint64
				lines := bufio.NewScanner(resp.Body)
				for lines.Scan() {
					metric, value, found := strings.Cut(lines.Text(), " ")
					if found && metric == "konnectivity_network_proxy_agent_open_server_connections" {
						openConns, err = strconv.ParseUint(value, 10, 64)
						if err != nil {
							lastErr = fmt.Errorf("failed to parse open server connections: %w", err)
							return false, nil
						}
						break
					}
				}
				if err := lines.Err(); err != nil {
					lastErr = fmt.Errorf("failed to parse metrics: %w", err)
					return false, nil
				}

				if openConns != uint64(numControllers) {
					lastErr = fmt.Errorf(
						"%d open konnectivity connections on %s, expected %d",
						openConns, pods.Items[0].Name, numControllers,
					)
					return false, nil
				}

				s.T().Log(openConns, "open konnectivity connections on", pods.Items[0].Name)
				return true, nil
			}
		})
		if err != nil {
			return fmt.Errorf("%w (%w)", err, lastErr)
		}
		return nil
	})

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
			WithLB:          true,
		},
	}
	testifysuite.Run(t, &s)
}
