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
package configchange

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/k0sproject/k0s/inttest/common"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	cfgClient "github.com/k0sproject/k0s/pkg/apis/k0s.k0sproject.io/clientset/typed/k0s.k0sproject.io/v1beta1"
	"github.com/k0sproject/k0s/pkg/apis/k0s.k0sproject.io/v1beta1"
	"github.com/k0sproject/k0s/pkg/constant"
)

type ConfigSuite struct {
	common.FootlooseSuite
}

func TestConfigSuite(t *testing.T) {
	s := ConfigSuite{
		common.FootlooseSuite{
			ControllerCount: 1,
			WorkerCount:     2,
		},
	}
	suite.Run(t, &s)
}

func (s *ConfigSuite) TestK0sGetsUp() {

	s.NoError(s.InitController(0, "--enable-dynamic-config"))
	s.NoError(s.RunWorkers())

	kc, err := s.KubeClient(s.ControllerNode(0))
	s.NoError(err)

	err = s.WaitForNodeReady(s.WorkerNode(0), kc)
	s.NoError(err)
	err = s.WaitForNodeReady(s.WorkerNode(1), kc)
	s.NoError(err)
	s.T().Log("waiting to see kube-router pods ready")
	s.NoError(common.WaitForKubeRouterReady(kc), "kube-router did not start")

	// Cluster is up-and-running, we can now start testing the config changes

	s.NoError(s.clearConfigEvents(kc))

	cfgClient, err := s.getConfigClient()
	s.Require().NoError(err)

	eventWatch, err := kc.CoreV1().Events("kube-system").Watch(context.Background(), metav1.ListOptions{FieldSelector: "involvedObject.name=k0s"})
	s.NoError(err)
	defer eventWatch.Stop()

	s.T().Run("changing cni should fail", func(t *testing.T) {
		originalConfig, err := cfgClient.Get(context.Background(), "k0s", metav1.GetOptions{})
		assert.NoError(t, err)
		newConfig := originalConfig.DeepCopy()
		newConfig.Spec.Network.Provider = constant.CNIProviderCalico
		newConfig.Spec.Network.Calico = v1beta1.DefaultCalico()
		newConfig.Spec.Network.KubeRouter = nil
		updatedConfig, err := cfgClient.Update(context.Background(), newConfig, metav1.UpdateOptions{})
		require.NoError(t, err)
		t.Log("Updated cluster config, resource version is", updatedConfig.ResourceVersion)

		// Check that we see proper event for failed reconcile
		event, err := s.waitForReconcileEvent(eventWatch)
		require.NoError(t, err)

		t.Logf("the event is %+v", event)
		assert.Equal(t, updatedConfig.ResourceVersion, event.InvolvedObject.ResourceVersion)
		assert.Equal(t, "Warning", event.Type)
		assert.Equal(t, "FailedReconciling", event.Reason)
		assert.Equal(t, "cannot change CNI provider from kuberouter to calico", event.Message)
	})

	s.T().Run("setting bad ip address should fail", func(t *testing.T) {
		originalConfig, err := cfgClient.Get(context.Background(), "k0s", metav1.GetOptions{})
		s.NoError(err)
		newConfig := originalConfig.DeepCopy()
		newConfig.Spec.Network = v1beta1.DefaultNetwork()
		newConfig.Spec.Network.PodCIDR = "invalid ip address"
		updatedConfig, err := cfgClient.Update(context.Background(), newConfig, metav1.UpdateOptions{})
		require.NoError(t, err)
		t.Log("Updated cluster config, resource version is", updatedConfig.ResourceVersion)

		// Check that we see proper event for failed reconcile
		event, err := s.waitForReconcileEvent(eventWatch)
		require.NoError(t, err)

		t.Logf("the event is %+v", event)
		assert.Equal(t, updatedConfig.ResourceVersion, event.InvolvedObject.ResourceVersion)
		assert.Equal(t, "Warning", event.Type)
		assert.Equal(t, "FailedReconciling", event.Reason)
		assert.Equal(t, "failed to validate config: invalid pod CIDR: invalid ip address", event.Message)
	})

	s.T().Run("changing kuberouter MTU should work", func(t *testing.T) {
		originalConfig, err := cfgClient.Get(context.Background(), "k0s", metav1.GetOptions{})
		assert.NoError(t, err)
		newConfig := originalConfig.DeepCopy()
		newConfig.Spec.Network = v1beta1.DefaultNetwork()
		newConfig.Spec.Network.KubeRouter.AutoMTU = false
		newConfig.Spec.Network.KubeRouter.MTU = 1300

		// Get the resource version for current kuberouter configmap
		cml, err := kc.CoreV1().ConfigMaps("kube-system").List(context.Background(), metav1.ListOptions{
			FieldSelector: fields.OneTermEqualSelector("metadata.name", "kube-router-cfg").String(),
		})
		assert.NoError(t, err)

		updatedConfig, err := cfgClient.Update(context.Background(), newConfig, metav1.UpdateOptions{})
		require.NoError(t, err)
		t.Log("Updated cluster config, resource version is", updatedConfig.ResourceVersion)

		event, err := s.waitForReconcileEvent(eventWatch)
		require.NoError(t, err)

		t.Logf("the event is %+v", event)
		assert.Equal(t, updatedConfig.ResourceVersion, event.InvolvedObject.ResourceVersion)
		assert.Equal(t, "Normal", event.Type)
		assert.Equal(t, "SuccessfulReconcile", event.Reason)
		assert.Equal(t, "successfully reconciled cluster config", event.Message)

		// Verify MTU setting have been propagated properly
		// It takes a while to actually apply the changes through stack applier
		// Start the watch only from last version so we only get changed cm(s) and not the original one
		w, err := kc.CoreV1().ConfigMaps("kube-system").Watch(context.Background(), metav1.ListOptions{
			FieldSelector:   fields.OneTermEqualSelector("metadata.name", "kube-router-cfg").String(),
			ResourceVersion: cml.ResourceVersion,
		})
		require.NoError(t, err)
		defer w.Stop()
		timeout := time.After(20 * time.Second)
		select {
		case e := <-w.ResultChan():
			cm := e.Object.(*v1.ConfigMap)
			cniConf := cm.Data["cni-conf.json"]
			assert.Contains(t, cniConf, `"mtu": 1300`)
			assert.Contains(t, cniConf, `"auto-mtu": false`)
		case <-timeout:
			t.FailNow()
		}
	})
}

func (s *ConfigSuite) waitForReconcileEvent(eventWatch watch.Interface) (*v1.Event, error) {
	timeout := time.After(20 * time.Second)
	select {
	case e := <-eventWatch.ResultChan():
		event := e.Object.(*v1.Event)
		return event, nil
	case <-timeout:
		return nil, fmt.Errorf("timeout waiting for reconcile event")
	}
}

func (s *ConfigSuite) clearConfigEvents(kc *kubernetes.Clientset) error {
	return kc.CoreV1().Events("kube-system").DeleteCollection(context.Background(), metav1.DeleteOptions{}, metav1.ListOptions{FieldSelector: "involvedObject.name=k0s"})
}

// Helper to dump the kubeconfig into temp file so we can load the clusterconfig client with it
func (s *ConfigSuite) getConfigClient() (cfgClient.ClusterConfigInterface, error) {
	kubeConfig, err := s.GetKubeClientConfig(s.ControllerNode(0))
	if err != nil {
		return nil, err
	}

	f, err := os.CreateTemp("", "kubeconfig-*")
	if err != nil {
		return nil, err
	}

	s.T().Logf("temp kubeconfig at %s", f.Name())

	err = clientcmd.WriteToFile(*kubeConfig, f.Name())
	if err != nil {
		return nil, err
	}

	config, err := clientcmd.BuildConfigFromFlags("", f.Name())
	if err != nil {
		return nil, fmt.Errorf("can't read kubeconfig: %v", err)
	}
	c, err := cfgClient.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("can't create kubernetes typed client for cluster config: %v", err)
	}
	return c.ClusterConfigs(constant.ClusterConfigNamespace), nil
}
