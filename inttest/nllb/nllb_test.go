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

package basic

import (
	"fmt"
	"testing"

	"github.com/k0sproject/k0s/inttest/common"
	"github.com/k0sproject/k0s/pkg/apis/k0s.k0sproject.io/v1beta1"
	"k8s.io/utils/pointer"

	testifysuite "github.com/stretchr/testify/suite"
	"sigs.k8s.io/yaml"
)

type suite struct {
	common.FootlooseSuite
}

func (s *suite) TestK0sGetsUp() {
	require := s.Require()

	config, err := yaml.Marshal(&v1beta1.ClusterConfig{
		Spec: &v1beta1.ClusterSpec{
			Network: func() *v1beta1.Network {
				network := v1beta1.DefaultNetwork(nil)
				network.NodeLocalLoadBalancer.Enabled = pointer.Bool(true)
				return network
			}(),
		},
	})
	require.NoError(err)

	s.WriteFileContent(s.ControllerNode(0), "/tmp/k0s.yaml", config)
	require.NoError(s.InitController(0, "--config=/tmp/k0s.yaml"))

	token, err := s.GetJoinToken("worker")
	require.NoError(err)
	require.NoError(s.RunWorkersWithToken(token))

	kc, err := s.KubeClient(s.ControllerNode(0))
	require.NoError(err)

	const kubeSystem = "kube-system"
	for i := 0; i < s.WorkerCount; i++ {
		nodeName := s.WorkerNode(i)
		nllbPodName := fmt.Sprintf("nllb-%s", nodeName)
		require.NoError(s.WaitForNodeReady(nodeName, kc))
		s.T().Logf("Waiting for pod %s/%s to become ready", kubeSystem, nllbPodName)
		require.NoError(
			common.WaitForPod(s.Context(), kc, nllbPodName, kubeSystem),
			"Pod %s/%s is not ready", kubeSystem, nllbPodName,
		)
	}

	require.NoError(common.WaitForKubeRouterReady(s.Context(), kc), "kube-router did not start")
	require.NoError(common.WaitForLease(s.Context(), kc, "kube-scheduler", kubeSystem))
	require.NoError(common.WaitForLease(s.Context(), kc, "kube-controller-manager", kubeSystem))

	// Test that we get logs, it's a signal that konnectivity tunnels work.
	s.T().Log("Waiting to get logs from pods")
	require.NoError(common.WaitForPodLogs(s.Context(), kc, kubeSystem))
}

func TestNodeLocalLoadBalancerSuite(t *testing.T) {
	s := suite{
		common.FootlooseSuite{
			ControllerCount: 1,
			WorkerCount:     2,
		},
	}
	testifysuite.Run(t, &s)
}
