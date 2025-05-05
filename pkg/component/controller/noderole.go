/*
Copyright 2021 k0s authors

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

package controller

import (
	"context"
	"encoding/json"
	"errors"
	"path"
	"time"

	"github.com/k0sproject/k0s/pkg/component/controller/leaderelector"
	"github.com/k0sproject/k0s/pkg/constant"
	k8sutil "github.com/k0sproject/k0s/pkg/kubernetes"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/metadata"
	"k8s.io/kubernetes/cmd/kubeadm/app/constants"

	"github.com/go-openapi/jsonpointer"
	"github.com/sirupsen/logrus"
)

// NodeRole implements the component interface to manage node role labels for worker nodes
type NodeRole struct {
	log logrus.FieldLogger

	kubeClientFactory k8sutil.ClientFactoryInterface
	leaderElector     leaderelector.Interface

	stop func()
}

// NewNodeRole creates new NodeRole reconciler
func NewNodeRole(clientFactory k8sutil.ClientFactoryInterface, leaderElector leaderelector.Interface) *NodeRole {
	return &NodeRole{
		log:               logrus.WithFields(logrus.Fields{"component": constant.NodeRoleComponentName}),
		kubeClientFactory: clientFactory,
		leaderElector:     leaderElector,
	}
}

// Init no-op
func (n *NodeRole) Init(_ context.Context) error {
	return nil
}

// Run checks and adds labels
func (n *NodeRole) Start(ctx context.Context) error {
	restConfig, err := n.kubeClientFactory.GetRESTConfig()
	if err != nil {
		return err
	}
	metaClient, err := metadata.NewForConfig(restConfig)
	if err != nil {
		return err
	}
	client := metaClient.Resource(corev1.SchemeGroupVersion.WithResource("nodes"))
	unlabeledControllerNodes := metav1.ListOptions{
		LabelSelector: labels.NewSelector().Add(
			mustRequirement(constant.K0SNodeRoleLabel, selection.Equals, "control-plane"),
			mustRequirement(constants.LabelNodeRoleControlPlane, selection.DoesNotExist),
		).String(),
	}
	controlPlanePatch, err := json.Marshal([]map[string]string{{
		"op":    "add",
		"path":  path.Join("/metadata/labels", jsonpointer.Escape(constants.LabelNodeRoleControlPlane)),
		"value": "true",
	}})
	runtime.Must(err)

	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})

	go func() {
		defer close(stopped)
		wait.UntilWithContext(ctx, func(ctx context.Context) {
			if !n.leaderElector.IsLeader() {
				return
			}

			nodes, err := client.List(ctx, unlabeledControllerNodes)
			if err != nil {
				if !errors.Is(err, context.Cause(ctx)) {
					n.log.WithError(err).Error("Failed to list nodes")
				}
				return
			}

			for _, node := range nodes.Items {
				_, err := client.Patch(ctx, node.Name, types.JSONPatchType, controlPlanePatch, metav1.PatchOptions{FieldManager: "k0s"})
				if err != nil {
					if !errors.Is(err, context.Cause(ctx)) {
						n.log.WithError(err).Error("Failed to add control-plane label to node ", node.Name)
					}
					continue
				}

				n.log.Infof("Added control-plane label to node %s", node.Name)
			}
		}, 1*time.Minute)
	}()

	n.stop = func() { cancel(); <-stopped }

	return nil
}

func mustRequirement(key string, op selection.Operator, vals ...string) labels.Requirement {
	r, err := labels.NewRequirement(key, op, vals)
	runtime.Must(err)
	return *r
}

// Stop no-op
func (n *NodeRole) Stop() error {
	if n.stop != nil {
		n.stop()
	}

	return nil
}
