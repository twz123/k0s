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
	"bytes"
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sync"
	"sync/atomic"
	"text/template"
	"time"

	"github.com/k0sproject/k0s/internal/pkg/dir"
	"github.com/k0sproject/k0s/pkg/apis/k0s.k0sproject.io/v1beta1"
	"github.com/k0sproject/k0s/pkg/applier"
	"github.com/k0sproject/k0s/pkg/component"
	"github.com/k0sproject/k0s/pkg/constant"
	"go.uber.org/multierr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/utils/pointer"

	"github.com/sirupsen/logrus"
)

type NodeLocalLoadBalancer struct {
	log logrus.FieldLogger

	configClient *KubeletConfigClient

	mu            sync.Mutex
	apiServersPtr atomic.Value
	statePtr      atomic.Value
}

var _ component.Component = (*NodeLocalLoadBalancer)(nil)

// NewNodeLocalLoadBalancer creates a new node_local_load_balancer component.
func NewNodeLocalLoadBalancer(cfg *constant.CfgVars, configClient *KubeletConfigClient, staticPods StaticPods) *NodeLocalLoadBalancer {
	nllb := &NodeLocalLoadBalancer{
		log: logrus.WithFields(logrus.Fields{"component": "node_local_load_balancer"}),

		configClient: configClient,
	}

	nllb.apiServersPtr.Store([]string{})
	nllb.store(&nllbCreated{staticPods, cfg.DataDir})
	return nllb
}

func (n *NodeLocalLoadBalancer) Init(context.Context) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	switch state := n.state().(type) {
	case *nllbCreated:
		return n.init(state)
	default:
		return fmt.Errorf("node_local_load_balancer component is already %s", state)
	}
}

func (n *NodeLocalLoadBalancer) Start(ctx context.Context) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	switch state := n.state().(type) {
	case *nllbCreated:
		return fmt.Errorf("node_local_load_balancer component is not yet initialized (%s)", state)
	case *nllbInitialized:
		return n.start(state)
	default:
		return fmt.Errorf("node_local_load_balancer component is already %s", state)
	}
}

func (n *NodeLocalLoadBalancer) Stop() error {
	n.mu.Lock()
	defer n.mu.Unlock()

	switch state := n.state().(type) {
	case *nllbCreated:
		n.store(&nllbStopped{})
		return nil
	case *nllbInitialized:
		return n.stop(state.nllbConfig)
	case *nllbStarted:
		state.stop()
		return n.stop(state.nllbConfig)
	case *nllbStopped:
		return nil
	default:
		return fmt.Errorf("node_local_load_balancer component is not yet started (%s)", state)
	}
}

func (n *NodeLocalLoadBalancer) Healthy() error {
	switch state := n.state().(type) {
	default:
		return fmt.Errorf("node_local_load_balancer component is not yet started (%s)", state)
	case *nllbStarted:
		return n.healthy(state)
	case *nllbStopped:
		return fmt.Errorf("node_local_load_balancer component is already %s", state)
	}
}

type nllbState interface {
	String() string
}

type nllbHostPort struct {
	Host string
	Port uint16
}

// func parseHostPort(address string, defaultPort uint16) (*nllbHostPort, error) {
// 	host, portStr, err := net.SplitHostPort(address)
// 	if err != nil {
// 		if defaultPort != 0 {
// 			addrErr := &net.AddrError{}
// 			if errors.As(err, &addrErr) && addrErr.Err == "missing port in address" {
// 				return &nllbHostPort{addrErr.Addr, defaultPort}, nil
// 			}
// 		}

// 		return nil, err
// 	}

// 	port, err := strconv.ParseUint(portStr, 10, 16)
// 	if err != nil {
// 		return nil, fmt.Errorf("invalid port number: %q: %w", portStr, err)
// 	}

// 	return &nllbHostPort{host, uint16(port)}, nil
// }

type nllbConfig struct {
	configDir       string
	loadBalancerPod StaticPod
}

type nllbRecoState struct {
	Shared struct {
		LBPort uint16
	}

	LoadBalancer struct {
		UpstreamServers []nllbHostPort
	}

	pod struct {
		image v1beta1.ImageSpec
	}
}

func (n *NodeLocalLoadBalancer) state() nllbState {
	return *n.statePtr.Load().(*nllbState)
}

func (n *NodeLocalLoadBalancer) store(state nllbState) {
	n.statePtr.Store(&state)
}

type nllbCreated struct {
	staticPods StaticPods
	dataDir    string
}

func (*nllbCreated) String() string {
	return "created"
}

type nllbInitialized struct {
	*nllbConfig
}

func (*nllbInitialized) String() string {
	return "initialized"
}

func (n *NodeLocalLoadBalancer) init(state *nllbCreated) error {
	configDir := filepath.Join(state.dataDir, "nllb")

	for _, folder := range []struct {
		path string
		mode os.FileMode
	}{
		{path: configDir, mode: 0700},
		{path: filepath.Join(configDir, "envoy"), mode: 0755},
	} {
		err := os.Mkdir(folder.path, folder.mode)
		if err == nil {
			continue
		}
		if !os.IsExist(err) || !dir.IsDirectory(folder.path) {
			return err
		}
		switch runtime.GOOS {
		case "windows", "plan9": // os.Chown unsupported
		default:
			if err = os.Chown(folder.path, os.Geteuid(), -1); err != nil {
				return err
			}
		}
		if err = os.Chmod(folder.path, folder.mode); err != nil {
			return err
		}
	}

	loadBalancerPod, err := state.staticPods.ClaimStaticPod("kube-system", "nllb")
	if err != nil {
		return fmt.Errorf("node_local_load_balancer: failed to claim static pod: %w", err)
	}

	initialized := &nllbInitialized{
		&nllbConfig{
			configDir:       configDir,
			loadBalancerPod: loadBalancerPod,
		},
	}

	n.store(initialized)
	return nil
}

func (n *NodeLocalLoadBalancer) start(state *nllbInitialized) error {
	ctx, cancel := context.WithCancel(context.Background())

	updates := make(chan nllbUpdateFunc)
	reconcileDone := make(chan struct{})
	watchDone := make(chan struct{})

	started := &nllbStarted{state, func() {
		cancel()
		<-watchDone
		close(updates)
		<-reconcileDone
	}}

	go func() {
		defer close(reconcileDone)
		n.reconcile(started.nllbConfig, updates)
	}()

	// FIXME needs to come from reconciliation
	updates <- func(state *nllbRecoState) {
		state.Shared.LBPort = 7443
		state.LoadBalancer.UpstreamServers = []nllbHostPort{{"127.0.0.1", 6443}}
		state.pod.image = v1beta1.DefaultClusterImages().EnvoyProxy
	}

	go func() {
		defer close(watchDone)
		wait.UntilWithContext(ctx, func(ctx context.Context) {
			err := n.configClient.WatchAPIServers(ctx, func(endpoints *corev1.Endpoints) error {
				n.updateAPIServers(endpoints, updates)
				return nil
			})
			if err != nil {
				n.log.WithError(err).Error("Failed to watch for API server addresses")
			}
		}, 10*time.Second)
	}()

	n.store(started)

	return nil
}

type nllbStarted struct {
	*nllbInitialized
	stop func()
}

func (*nllbStarted) String() string {
	return "started"
}

type nllbUpdateFunc func(*nllbRecoState)

func (n *NodeLocalLoadBalancer) reconcile(c *nllbConfig, updates <-chan nllbUpdateFunc) {
	for state := (nllbRecoState{}); ; {
		updateFunc, ok := <-updates
		if !ok {
			return
		}

		oldState := state
		updateFunc(&state)

		changed := state.Shared != oldState.Shared

		if changed || reflect.DeepEqual(state.LoadBalancer, oldState.LoadBalancer) {
			n.updateLoadBalancerConfig(c, &state)
		}

		if changed || state.pod != oldState.pod {
			if err := n.provision(c, &state); err != nil {
				n.log.WithError(err).Error("Failed to reconcile node-local load balancer")
			}
		}
	}
}

func (n *NodeLocalLoadBalancer) updateLoadBalancerConfig(c *nllbConfig, state *nllbRecoState) {
	envoyDir := filepath.Join(c.configDir, "envoy")
	newEnvoyConfigFile, err := n.createNewEnvoyConfigFile(envoyDir, state)
	if err != nil {
		n.log.WithError(err).Error("Failed to create updated Envoy configuration")
	}

	err = os.Rename(newEnvoyConfigFile, filepath.Join(envoyDir, "envoy.yaml"))
	if err != nil {
		err = multierr.Append(err, os.Remove(newEnvoyConfigFile))
		n.log.WithError(err).Error("Failed to replace Envoy configuration file")
	}

	n.log.Info("Load balancer configuration updated")
}

// admin:
//   address:
//     socket_address: { address: 127.0.0.1, port_value: {{ .AdminPort }} }

var envoyBootstrapConfig = template.Must(template.New("Bootstrap").Parse(`
static_resources:
  listeners:
  - name: apiserver
    address:
      socket_address: { address: 127.0.0.1, port_value: {{ .Shared.LBPort }} }
    filter_chains:
    - filters:
      - name: envoy.filters.network.tcp_proxy
        typed_config:
          "@type": type.googleapis.com/envoy.extensions.filters.network.tcp_proxy.v3.TcpProxy
          stat_prefix: apiserver
          cluster: apiserver
  clusters:
  - name: apiserver
    connect_timeout: 0.25s
    type: STATIC
    lb_policy: RANDOM
    load_assignment:
      cluster_name: apiserver
      endpoints:
      - lb_endpoints:
        {{- range .LoadBalancer.UpstreamServers }}
        - endpoint:
            address:
              socket_address:
                address: {{ printf "%q" .Host }}
                port_value: {{ .Port }}
        {{- else }} []{{ end }}
    health_checks:
    # FIXME: Better use a proper HTTP based health check, but this needs certs and stuff...
    - tcp_health_check: {}
      timeout: 1s
      interval: 5s
      healthy_threshold: 3
      unhealthy_threshold: 5
`))

func (n *NodeLocalLoadBalancer) provision(c *nllbConfig, state *nllbRecoState) error {
	manifest := corev1.Pod{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Pod",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "nllb",
			Namespace: "kube-system",
			Labels:    applier.CommonLabels("nllb"),
		},
		Spec: corev1.PodSpec{
			HostNetwork: true,
			SecurityContext: &corev1.PodSecurityContext{
				RunAsNonRoot: pointer.Bool(true),
			},
			Containers: []corev1.Container{{
				Name:  "nllb",
				Image: state.pod.image.URI(),
				Ports: []corev1.ContainerPort{
					{Name: "nllb", ContainerPort: 80, Protocol: corev1.ProtocolTCP},
				},
				SecurityContext: &corev1.SecurityContext{
					ReadOnlyRootFilesystem:   pointer.Bool(true),
					Privileged:               pointer.Bool(false),
					AllowPrivilegeEscalation: pointer.Bool(false),
					Capabilities: &corev1.Capabilities{
						Drop: []corev1.Capability{"ALL"},
					},
				},
				VolumeMounts: []corev1.VolumeMount{{
					Name:      "envoy-config",
					MountPath: "/etc/envoy",
					ReadOnly:  true,
				}},
				LivenessProbe: &corev1.Probe{
					PeriodSeconds:    10,
					FailureThreshold: 3,
					TimeoutSeconds:   3,
					ProbeHandler: corev1.ProbeHandler{
						TCPSocket: &corev1.TCPSocketAction{
							Host: "127.0.0.1", Port: intstr.FromInt(int(state.Shared.LBPort)),
						},
					},
				},
			}},
			Volumes: []corev1.Volume{{
				Name: "envoy-config",
				VolumeSource: corev1.VolumeSource{
					HostPath: &corev1.HostPathVolumeSource{
						Path: filepath.Join(c.configDir, "envoy"),
						Type: (*corev1.HostPathType)(pointer.String(string(corev1.HostPathDirectory))),
					},
				}},
			},
		},
	}

	if err := c.loadBalancerPod.SetManifest(manifest); err != nil {
		return err
	}

	n.log.Info("Provisioned static load balancer Pod")
	return nil
}

func (n *NodeLocalLoadBalancer) createNewEnvoyConfigFile(dir string, state *nllbRecoState) (string, error) {
	var envoyConfig bytes.Buffer
	if err := envoyBootstrapConfig.Execute(&envoyConfig, state); err != nil {
		return "", fmt.Errorf("failed to generate envoy configuration: %w", err)
	}

	fd, err := os.CreateTemp(dir, ".envoy-*.yaml.tmp")
	if err != nil {
		return "", err
	}

	newConfigFile := fd.Name()
	_, err = fd.Write(envoyConfig.Bytes())
	if err == nil {
		err = fd.Sync()
	}

	err = multierr.Append(err, fd.Close())
	if err == nil {
		err = os.Chmod(newConfigFile, 0444)
	}

	if err != nil {
		return "", multierr.Append(err, os.Remove(newConfigFile))
	}

	n.log.Debugf("Wrote new Envoy configuration: %s: %s", newConfigFile, envoyConfig)
	return newConfigFile, nil
}

func (n *NodeLocalLoadBalancer) updateAPIServers(endpoints *corev1.Endpoints, updates chan<- nllbUpdateFunc) {
	apiServers := []nllbHostPort{}
	for sIdx, subset := range endpoints.Subsets {
		var ports []uint16
		for _, port := range subset.Ports {
			// FIXME: is a more sophisticated port detection required?
			// E.g. does the service object need to be inspected?
			if port.Protocol == corev1.ProtocolTCP && port.Name == "https" {
				if port.Port > 0 && port.Port <= math.MaxUint16 {
					ports = append(ports, uint16(port.Port))
				}
			}
		}

		if len(ports) < 1 {
			n.log.Warnf("No suitable ports found in subset %d: %+#v", sIdx, subset.Ports)
			continue
		}

		for aIdx, address := range subset.Addresses {
			host := address.IP
			if host == "" {
				host = address.Hostname
			}
			if host == "" {
				n.log.Warnf("Failed to get host from address %d/%d: %+#v", sIdx, aIdx, address)
				continue
			}

			for _, port := range ports {
				apiServers = append(apiServers, nllbHostPort{host, port})
			}
		}
	}

	if len(apiServers) < 1 {
		// Never update the API servers with an empty list. This cannot
		// be right in any case, and would never recover.
		n.log.Warn("No API server addresses discovered")
		return
	}

	updates <- func(state *nllbRecoState) {
		n.log.Debug("Updating API server addresses:", apiServers)
		state.LoadBalancer.UpstreamServers = apiServers
	}
}

func (n *NodeLocalLoadBalancer) healthy(state *nllbStarted) error {
	// req, err := http.NewRequest(http.MethodGet, healthCheckURL, nil)
	// if err != nil {
	// 	return fmt.Errorf("node_local_load_balancer: health check failed: %w", err)
	// }

	// ctx, cancel := context.WithTimeout(context.TODO(), 3*time.Second)
	// defer cancel()

	// resp, err := http.DefaultClient.Do(req.WithContext(ctx))
	// if err != nil {
	// 	return fmt.Errorf("node_local_load_balancer: health check failed: %w", err)
	// }
	// resp.Body.Close()
	// if resp.StatusCode != http.StatusNoContent {
	// 	return fmt.Errorf("unexpected HTTP response status: %s", resp.Status)
	// }

	return nil
}

func (n *NodeLocalLoadBalancer) stop(config *nllbConfig) error {
	config.loadBalancerPod.Drop()

	configFile := filepath.Join(config.configDir, "envoy", "envoy.yaml")
	if err := os.Remove(configFile); err != nil && !os.IsNotExist(err) {
		n.log.WithError(err).Warn("Failed to remove envoy config from disk")
	}

	n.store(&nllbStopped{})
	return nil
}

type nllbStopped struct{}

func (*nllbStopped) String() string {
	return "stopped"
}
