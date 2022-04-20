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

package worker

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/k0sproject/k0s/pkg/component"

	"github.com/sirupsen/logrus"
)

type NodeLocalLoadBalancer interface {
	UpdateAPIServers(apiServers []string)
}

type nodeLocalLoadBalancer struct {
	log logrus.FieldLogger

	mu            sync.Mutex
	apiServersPtr atomic.Value
	statePtr      atomic.Value
}

type nllbState interface {
	String() string
}

type nllbCreated struct {
	staticPods StaticPods
}

func (*nllbCreated) String() string {
	return "created"
}

// NewNodeLocalLoadBalancer creates a new node_local_load_balancer component.
func NewNodeLocalLoadBalancer(staticPods StaticPods) interface {
	component.Component
	NodeLocalLoadBalancer
} {
	nodeLocalLoadBalancer := &nodeLocalLoadBalancer{
		log: logrus.WithFields(logrus.Fields{"component": "node_local_load_balancer"}),
	}

	nodeLocalLoadBalancer.apiServersPtr.Store([]string{})
	nodeLocalLoadBalancer.store(&nllbCreated{staticPods})
	return nodeLocalLoadBalancer
}

func (n *nodeLocalLoadBalancer) UpdateAPIServers(apiServers []string) {
	n.apiServersPtr.Store(append([]string(nil), apiServers...))
}

func (n *nodeLocalLoadBalancer) Init(context.Context) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	switch state := n.state().(type) {
	case *nllbCreated:
		loadBalancerPod, err := state.staticPods.ClaimStaticPod("kube-system", "nllb")
		if err != nil {
			return fmt.Errorf("node_local_load_balancer: failed to claim static pod: %w", err)
		}

		n.store(&nllbInitialized{loadBalancerPod})
		return nil
	default:
		return fmt.Errorf("node_local_load_balancer component is already %s", state)
	}
}

type nllbInitialized struct {
	loadBalancerPod StaticPod
}

func (*nllbInitialized) String() string {
	return "initialized"
}

const lbPodManifest = `
apiVersion: v1
kind: Pod
metadata:
  name: nllb
  namespace: kube-system
spec:
  containers:
  - image: nginx
    name: web
    ports:
    - containerPort: 80
      name: web
      protocol: TCP
`

func (n *nodeLocalLoadBalancer) Run(ctx context.Context) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	switch state := n.state().(type) {
	case *nllbCreated:
		return fmt.Errorf("node_local_load_balancer component is not yet initialized (%s)", state)
	case *nllbInitialized:
		if err := state.loadBalancerPod.SetManifest(lbPodManifest); err != nil {
			return fmt.Errorf("node_local_load_balancer: failed to provision pod: %w", err)
		}
		n.store(&nllbRunning{
			state.loadBalancerPod,
			"FIXME:healthCheckURL",
		})
		n.log.Info("Provisioned static load balancer pod")
		return nil
	default:
		return fmt.Errorf("node_local_load_balancer component is already %s", state)
	}
}

type nllbRunning struct {
	loadBalancerPod StaticPod
	healthCheckURL  string
}

func (*nllbRunning) String() string {
	return "running"
}

func (n *nodeLocalLoadBalancer) Stop() error {
	n.mu.Lock()
	defer n.mu.Unlock()

	switch state := n.state().(type) {
	case *nllbRunning:
		state.loadBalancerPod.Drop()
		n.store(&nllbStopped{})
		return nil
	case *nllbStopped:
		return nil
	default:
		return fmt.Errorf("node_local_load_balancer component is not yet running (%s)", state)
	}
}

type nllbStopped struct{}

func (*nllbStopped) String() string {
	return "stopped"
}

func (n *nodeLocalLoadBalancer) Healthy() error {
	var healthCheckURL string
	switch state := n.state().(type) {
	default:
		return fmt.Errorf("node_local_load_balancer component is not yet running (%s)", state)
	case *nllbRunning:
		healthCheckURL = state.healthCheckURL
	case *nllbStopped:
		return fmt.Errorf("node_local_load_balancer component is already %s", state)
	}

	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf(healthCheckURL), nil)
	if err != nil {
		return fmt.Errorf("node_local_load_balancer: health check failed: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.TODO(), 3*time.Second)
	defer cancel()

	resp, err := http.DefaultClient.Do(req.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("node_local_load_balancer: health check failed: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("unexpected HTTP response status: %s", resp.Status)
	}
	return nil
}

func (n *nodeLocalLoadBalancer) Reconcile() error {
	_, ok := n.state().(*nllbRunning)
	if !ok {
		return errors.New("node_local_load_balancer in wrong state")
	}

	n.log.Info("Reconciling") // FIXME Debug?
	return nil
}

func (n *nodeLocalLoadBalancer) state() nllbState {
	return *n.statePtr.Load().(*nllbState)
}

func (n *nodeLocalLoadBalancer) store(state nllbState) {
	n.statePtr.Store(&state)
}
