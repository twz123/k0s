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
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"text/template"

	"github.com/k0sproject/k0s/internal/pkg/dir"
	"github.com/k0sproject/k0s/pkg/apis/k0s.k0sproject.io/v1beta1"
	"github.com/k0sproject/k0s/pkg/component"
	"github.com/k0sproject/k0s/pkg/constant"
	"go.uber.org/multierr"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/pointer"

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

// NewNodeLocalLoadBalancer creates a new node_local_load_balancer component.
func NewNodeLocalLoadBalancer(cfg *constant.CfgVars, staticPods StaticPods) interface {
	component.ReconcilerComponent
	NodeLocalLoadBalancer
} {
	nllb := &nodeLocalLoadBalancer{
		log: logrus.WithFields(logrus.Fields{"component": "node_local_load_balancer"}),
	}

	nllb.apiServersPtr.Store([]string{})
	nllb.store(&nllbCreated{staticPods, cfg.DataDir})
	return nllb
}

func (n *nodeLocalLoadBalancer) UpdateAPIServers(apiServers []string) {
	n.apiServersPtr.Store(append([]string(nil), apiServers...))
}

func (n *nodeLocalLoadBalancer) Init(context.Context) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	switch state := n.state().(type) {
	case *nllbCreated:
		return n.init(state)
	default:
		return fmt.Errorf("node_local_load_balancer component is already %s", state)
	}
}

func (n *nodeLocalLoadBalancer) Run(ctx context.Context) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	switch state := n.state().(type) {
	case *nllbCreated:
		return fmt.Errorf("node_local_load_balancer component is not yet initialized (%s)", state)
	case *nllbInitialized:
		return n.run(state)
	default:
		return fmt.Errorf("node_local_load_balancer component is already %s", state)
	}
}

func (n *nodeLocalLoadBalancer) Stop() error {
	n.mu.Lock()
	defer n.mu.Unlock()

	switch state := n.state().(type) {
	case *nllbCreated:
		n.store(&nllbStopped{})
		return nil
	case *nllbInitialized:
		return n.stop(state.nllbConfig)
	case *nllbRunning:
		return n.stop(state.nllbConfig)
	case *nllbStopped:
		return nil
	default:
		return fmt.Errorf("node_local_load_balancer component is not yet running (%s)", state)
	}
}

func (n *nodeLocalLoadBalancer) Healthy() error {
	switch state := n.state().(type) {
	default:
		return fmt.Errorf("node_local_load_balancer component is not yet running (%s)", state)
	case *nllbRunning:
		return n.healthy(state)
	case *nllbStopped:
		return fmt.Errorf("node_local_load_balancer component is already %s", state)
	}
}

func (n *nodeLocalLoadBalancer) Reconcile(_ context.Context, cfg *v1beta1.ClusterConfig) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	switch state := n.state().(type) {
	case *nllbInitialized:
		return n.reconcile(state.nllbConfig, &state.nllbPodSpec, cfg.Spec)
	case *nllbRunning:
		if err := n.reconcile(state.nllbConfig, &state.nllbPodSpec, cfg.Spec); err != nil {
			return err
		}
		return n.provision(state)
	default:
		return fmt.Errorf("node_local_load_balancer: cannot reconcile: %s", state)
	}
}

type nllbState interface {
	String() string
}

type nllbHostPort struct {
	Host string
	Port uint16
}

func parseHostPort(address string, defaultPort uint16) (*nllbHostPort, error) {
	host, portStr, err := net.SplitHostPort(address)
	if err != nil {
		if defaultPort != 0 {
			addrErr := &net.AddrError{}
			if errors.As(err, &addrErr) && addrErr.Err == "missing port in address" {
				return &nllbHostPort{addrErr.Addr, defaultPort}, nil
			}
		}

		return nil, err
	}

	port, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil {
		return nil, fmt.Errorf("invalid port number: %q: %w", portStr, err)
	}

	return &nllbHostPort{host, uint16(port)}, nil
}

type nllbConfig struct {
	configDir       string
	loadBalancerPod StaticPod
}

type nllbPodSpec struct {
	Image      v1beta1.ImageSpec
	LocalAddr  string
	LBPort     uint16
	APIServers []nllbHostPort
}

func (n *nodeLocalLoadBalancer) state() nllbState {
	return *n.statePtr.Load().(*nllbState)
}

func (n *nodeLocalLoadBalancer) store(state nllbState) {
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
	*nllbPodSpec
}

func (*nllbInitialized) String() string {
	return "initialized"
}

func (n *nodeLocalLoadBalancer) init(state *nllbCreated) error {
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

	n.store(&nllbInitialized{
		&nllbConfig{
			configDir:       configDir,
			loadBalancerPod: loadBalancerPod,
		},
		nil,
	})
	return nil
}

func (n *nodeLocalLoadBalancer) reconcile(c *nllbConfig, podSpec **nllbPodSpec, cluster *v1beta1.ClusterSpec) error {
	apiServer, err := parseHostPort(cluster.API.Address, 6443)
	if err != nil {
		return fmt.Errorf("invalid API server address: %q: %w", cluster.API.Address, err)
	}

	if *podSpec == nil {
		*podSpec = &nllbPodSpec{
			// FIXME
			LocalAddr: "127.0.0.1",
			LBPort:    7443,
		}
	}

	var needsUpdate bool
	if (*podSpec).Image != cluster.Images.EnvoyProxy {
		(*podSpec).Image = cluster.Images.EnvoyProxy
		needsUpdate = true
	}
	if !reflect.DeepEqual((*podSpec).APIServers, []nllbHostPort{*apiServer}) {
		(*podSpec).APIServers = []nllbHostPort{*apiServer}
		needsUpdate = true
	}

	if !needsUpdate {
		n.log.Debugf("Envoy configuration up to date")
		return nil
	}

	envoyDir := filepath.Join(c.configDir, "envoy")
	if err := func() error {
		newEnvoyConfigFile, err := n.createNewEnvoyConfigFile(envoyDir, *podSpec)
		if err != nil {
			return err
		}

		err = os.Rename(newEnvoyConfigFile, filepath.Join(envoyDir, "envoy.yaml"))
		if err != nil {
			return multierr.Append(err, os.Remove(newEnvoyConfigFile))
		}

		return nil
	}(); err != nil {
		return fmt.Errorf("failed to replace envoy configuration: %w", err)
	}

	n.log.Info("Envoy configuration file replaced with an updated version")
	return nil
}

func (n *nodeLocalLoadBalancer) run(state *nllbInitialized) error {
	running := &nllbRunning{state}

	if err := n.provision(running); err != nil {
		return fmt.Errorf("node_local_load_balancer: cannot run: %w", err)
	}

	n.store(running)
	n.log.Info("Provisioned load balancer Pod")
	return nil
}

type nllbRunning struct {
	*nllbInitialized
}

func (*nllbRunning) String() string {
	return "running"
}

// admin:
//   address:
//     socket_address: { address: {{ printf "%q" .LocalAddr }}, port_value: {{ .AdminPort }} }

var envoyBootstrapConfig = template.Must(template.New("Bootstrap").Parse(`
static_resources:
  listeners:
  - name: apiserver
    address:
      socket_address: { address: {{ printf "%q" .LocalAddr }}, port_value: {{ .LBPort }} }
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
        {{- range .APIServers }}
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

func (n *nodeLocalLoadBalancer) provision(state *nllbRunning) error {
	podSpec := state.nllbPodSpec

	if podSpec == nil {
		return errors.New("not yet reconciled")
	}

	manifest := corev1.Pod{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Pod",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "nllb",
			Namespace: "kube-system",
		},
		Spec: corev1.PodSpec{
			HostNetwork: true,
			SecurityContext: &corev1.PodSecurityContext{
				RunAsNonRoot: pointer.Bool(true),
			},
			Containers: []corev1.Container{{
				Name:  "nllb",
				Image: podSpec.Image.URI(),
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
							Host: "127.0.0.1", Port: intstr.FromInt(int(podSpec.LBPort)),
						},
					},
				},
			}},
			Volumes: []corev1.Volume{{
				Name: "envoy-config",
				VolumeSource: corev1.VolumeSource{
					HostPath: &corev1.HostPathVolumeSource{
						Path: filepath.Join(state.configDir, "envoy"),
						Type: (*corev1.HostPathType)(pointer.String(string(corev1.HostPathDirectory))),
					},
				}},
			},
		},
	}

	return state.loadBalancerPod.SetManifest(manifest)
}

func (n *nodeLocalLoadBalancer) createNewEnvoyConfigFile(dir string, reconciled *nllbPodSpec) (string, error) {
	var envoyConfig bytes.Buffer
	if err := envoyBootstrapConfig.Execute(&envoyConfig, reconciled); err != nil {
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

	n.log.Debugf("Wrote new envoy configuration: %s", newConfigFile, envoyConfig)
	return newConfigFile, nil
}

func (n *nodeLocalLoadBalancer) healthy(state *nllbRunning) error {
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

func (n *nodeLocalLoadBalancer) stop(config *nllbConfig) error {
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
