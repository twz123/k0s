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
	"path"
	"time"

	"github.com/go-openapi/jsonpointer"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/kubernetes/cmd/kubeadm/app/constants"

	"github.com/k0sproject/k0s/pkg/config"
	"github.com/k0sproject/k0s/pkg/constant"
	k8sutil "github.com/k0sproject/k0s/pkg/kubernetes"
)

// NodeRole implements the component interface to manage node role labels for worker nodes
type NodeRole struct {
	log logrus.FieldLogger

	kubeClientFactory k8sutil.ClientFactoryInterface
	k0sVars           *config.CfgVars
}

// NewNodeRole creates new NodeRole reconciler
func NewNodeRole(k0sVars *config.CfgVars, clientFactory k8sutil.ClientFactoryInterface) *NodeRole {
	return &NodeRole{
		log: logrus.WithFields(logrus.Fields{"component": "noderole"}),

		kubeClientFactory: clientFactory,
		k0sVars:           k0sVars,
	}
}

// Init no-op
func (n *NodeRole) Init(_ context.Context) error {
	return nil
}

// Run checks and adds labels
func (n *NodeRole) Start(ctx context.Context) error {
	client, err := n.kubeClientFactory.GetClient()
	if err != nil {
		return err
	}
	go func() {
		timer := time.NewTicker(1 * time.Minute)
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				nodes, err := client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
				if err != nil {
					n.log.Errorf("failed to get node list: %v", err)
					continue
				}

				for _, node := range nodes.Items {
					err = n.ensureNodeLabel(ctx, client, node)
					if err != nil {
						n.log.Error(err)
					}
				}
			}
		}
	}()

	return nil
}

func (n *NodeRole) ensureNodeLabel(ctx context.Context, client kubernetes.Interface, node corev1.Node) error {
	if _, exists := node.Labels[constants.LabelNodeRoleControlPlane]; exists {
		return nil
	}

	if node.Labels[constant.K0SNodeRoleLabel] != "control-plane" {
		return nil
	}

	patch, err := json.Marshal([]map[string]string{{
		"op":    "add",
		"path":  path.Join("/metadata/labels", jsonpointer.Escape(constants.LabelNodeRoleControlPlane)),
		"value": "true",
	}})
	if err != nil {
		return err
	}

	_, err = client.CoreV1().Nodes().Patch(ctx, node.Name, types.JSONPatchType, []byte(patch), metav1.PatchOptions{})
	return err
}

// Stop no-op
func (n *NodeRole) Stop() error {
	return nil
}
