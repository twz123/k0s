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

package nllb

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"text/template"
	"time"

	"github.com/k0sproject/k0s/internal/pkg/dir"
	"github.com/k0sproject/k0s/internal/pkg/file"
	"github.com/k0sproject/k0s/pkg/apis/k0s.k0sproject.io/v1beta1"
	"github.com/k0sproject/k0s/pkg/applier"
	"github.com/k0sproject/k0s/pkg/component"
	"github.com/k0sproject/k0s/pkg/component/worker"
	"github.com/k0sproject/k0s/pkg/constant"
	kubeutil "github.com/k0sproject/k0s/pkg/kubernetes"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/pointer"

	"github.com/sirupsen/logrus"
	"sigs.k8s.io/yaml"
)

// NllbReconciler reconciles a static Pod on a worker node that implements
// node-local load balancing.
type NllbReconciler struct {
	log logrus.FieldLogger

	envoyProxy            *v1beta1.EnvoyProxy
	konnectivityAgentPort uint16
	pullPolicy            corev1.PullPolicy
	kubeconfigPaths       kubeconfigPaths

	mu       sync.Mutex
	statePtr atomic.Pointer[reconcilerState]
}

var _ component.Component = (*NllbReconciler)(nil)
var _ component.Healthz = (*NllbReconciler)(nil)

// NewReconciler creates a component that reconciles a static Pod that
// implements node-local load balancing.
func NewReconciler(
	cfg *constant.CfgVars,
	staticPods worker.StaticPods,
	nllb *v1beta1.NodeLocalLoadBalancer,
	konnectivityAgentPort uint16,
	pullPolicy corev1.PullPolicy,
) (*NllbReconciler, error) {

	if nllb.Type != v1beta1.NllbTypeEnvoyProxy {
		return nil, fmt.Errorf("unsupported node-local load balancing type: %q", nllb.Type)
	}

	envoyProxy := nllb.EnvoyProxy.DeepCopy()
	if envoyProxy == nil || envoyProxy.Image == nil {
		return nil, errors.New("configuration invalid: .envoyProxy.image unspecified")
	}

	dataDir := filepath.Join(cfg.DataDir, "nllb")
	r := &NllbReconciler{
		log: logrus.WithFields(logrus.Fields{"component": "node_local_load_balancer"}),

		envoyProxy:            envoyProxy,
		konnectivityAgentPort: konnectivityAgentPort,
		pullPolicy:            pullPolicy,
		kubeconfigPaths: kubeconfigPaths{
			regular: kubeconfigPath{
				regular:      cfg.KubeletAuthConfigPath,
				loadBalanced: filepath.Join(dataDir, "kubeconfig.yaml"),
			},
			bootstrap: kubeconfigPath{
				regular:      cfg.KubeletBootstrapConfigPath,
				loadBalanced: filepath.Join(dataDir, "bootstrap-kubeconfig.yaml"),
			},
		},
	}

	r.store(&reconcilerCreated{
		staticPods,
		dataDir,
	})

	return r, nil
}

func (r *NllbReconciler) GetKubeletKubeconfig() worker.KubeletKubeconfig {
	return worker.KubeletKubeconfig{
		Path:          r.kubeconfigPaths.regular.loadBalanced,
		BootstrapPath: r.kubeconfigPaths.bootstrap.regular,
	}
}

// NewClient returns a new Kubernetes client, backed by the node-local load balancer.
func (r *NllbReconciler) NewClient() (kubernetes.Interface, error) {
	return kubeutil.NewClient(kubeutil.FirstExistingKubeconfig(r.kubeconfigPaths.loadBalancedPaths()...))
}

func (r *NllbReconciler) Init(context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	switch state := r.state().(type) {
	case *reconcilerCreated:
		return r.init(state)
	default:
		return fmt.Errorf("node_local_load_balancer component is already %s", state)
	}
}

func (r *NllbReconciler) Start(context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	switch state := r.state().(type) {
	case *reconcilerCreated:
		return fmt.Errorf("node_local_load_balancer component is not yet initialized (%s)", state)
	case *reconcilerInitialized:
		return r.start(state)
	default:
		return fmt.Errorf("node_local_load_balancer component is already %s", state)
	}
}

func (r *NllbReconciler) Stop() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	switch state := r.state().(type) {
	case *reconcilerCreated:
		r.store(&nllbStopped{})
		return nil
	case *reconcilerInitialized:
		r.stop(state.reconcilerConfig)
		return nil
	case *reconcilerStarted:
		state.stop()
		r.stop(state.reconcilerConfig)
		return nil
	case *nllbStopped:
		return nil
	default:
		return fmt.Errorf("node_local_load_balancer component cannot stop (%s)", state)
	}
}

func (r *NllbReconciler) Healthy() error {
	switch state := r.state().(type) {
	default:
		return fmt.Errorf("node_local_load_balancer component is not yet started (%s)", state)
	case *reconcilerStarted:
		return r.healthy(state)
	case *nllbStopped:
		return fmt.Errorf("node_local_load_balancer component is already %s", state)
	}
}

type reconcilerState interface {
	String() string
}

type reconcilerConfig struct {
	envoyDir                 string
	apiServersBackupFilePath string
	loadBalancerPod          worker.StaticPod
}

type podState struct {
	Shared struct {
		LBAddr                    net.IP
		APIServerBindPort         uint16
		KonnectivityAgentBindPort uint16
	}

	LoadBalancer struct {
		KonnectivityServerPort uint16
		UpstreamServers        []hostPort
	}

	pod struct {
		image      v1beta1.ImageSpec
		pullPolicy corev1.PullPolicy
	}
}

func (s *podState) DeepCopy() *podState {
	if s == nil {
		return nil
	}
	out := new(podState)
	s.DeepCopyInto(out)
	return out
}

func (s *podState) DeepCopyInto(out *podState) {
	*out = *s
	out.LoadBalancer.UpstreamServers = append([]hostPort(nil), s.LoadBalancer.UpstreamServers...)
	s.pod.image.DeepCopyInto(&out.pod.image)
}

func (r *NllbReconciler) state() reconcilerState {
	return *r.statePtr.Load()
}

func (r *NllbReconciler) store(state reconcilerState) {
	r.statePtr.Store(&state)
}

type reconcilerCreated struct {
	staticPods worker.StaticPods
	dataDir    string
}

func (*reconcilerCreated) String() string {
	return "created"
}

type reconcilerInitialized struct {
	*reconcilerConfig
}

func (*reconcilerInitialized) String() string {
	return "initialized"
}

func (r *NllbReconciler) init(created *reconcilerCreated) error {
	loadBalancerPod, err := created.staticPods.ClaimStaticPod("kube-system", "nllb")
	if err != nil {
		return fmt.Errorf("failed to claim static pod for node-local load balancing: %w", err)
	}

	envoyDir := filepath.Join(created.dataDir, "envoy")
	for _, folder := range []struct {
		path string
		mode os.FileMode
	}{
		{path: created.dataDir, mode: 0700},
		{path: envoyDir, mode: 0755},
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

	initialized := &reconcilerInitialized{
		&reconcilerConfig{
			envoyDir:                 envoyDir,
			apiServersBackupFilePath: filepath.Join(created.dataDir, "api-servers.yaml"),
			loadBalancerPod:          loadBalancerPod,
		},
	}

	r.store(initialized)
	return nil
}

func (r *NllbReconciler) start(initialized *reconcilerInitialized) error {
	loopbackIP, err := getLoopbackIP()
	if err != nil {
		r.log.WithError(err).Infof("Falling back to %s as bind address", loopbackIP)
	}

	apiServers := []hostPort{}
	if apiServersBytes, err := os.ReadFile(initialized.apiServersBackupFilePath); !os.IsNotExist(err) {
		if err == nil {
			err = yaml.Unmarshal(apiServersBytes, &apiServers)
		}

		if err != nil {
			r.log.WithError(err).Warn("Failed to load cached API server addresses")
		}
	}

	if len(apiServers) < 1 {
		apiServer, err := r.loadAPIServerAddressFromKubeconfig()
		if err != nil {
			return fmt.Errorf("failed to obtain initial API server address from kubeconfig: %w", err)
		}
		apiServers = []hostPort{*apiServer}
	}

	ctx, cancel := context.WithCancel(context.Background())

	updates := make(chan podStateUpdateFunc, 1)
	fileReconcileDone := make(chan struct{}, 1)
	reconcileDone := make(chan struct{}, 1)
	watchDone := make(chan struct{}, 1)

	started := &reconcilerStarted{initialized.reconcilerConfig, func() {
		cancel()
		<-watchDone
		close(updates)
		<-reconcileDone
		<-fileReconcileDone
	}}

	go func() {
		defer close(reconcileDone)
		r.reconcile(started.reconcilerConfig, updates)
	}()

	go func() {
		defer close(fileReconcileDone)

		lbAddr := fmt.Sprintf("%s:%d", loopbackIP, r.envoyProxy.APIServerBindPort)

		wait.UntilWithContext(ctx, func(ctx context.Context) {
			r.log.Info("Starting to reconcile kubeconfig files")
			if err := r.kubeconfigPaths.reconcile(ctx, r.log, lbAddr, nil); err != nil {
				r.log.WithError(err).Error("Kubeconfig file reconciliation terminated erroneously")
			}

		}, 60*time.Second)
	}()

	updates <- func(state *podState) {
		state.Shared.LBAddr = loopbackIP
		state.Shared.APIServerBindPort = uint16(r.envoyProxy.APIServerBindPort)
		if r.envoyProxy.KonnectivityAgentBindPort != nil {
			state.Shared.KonnectivityAgentBindPort = uint16(*r.envoyProxy.KonnectivityAgentBindPort)
		}
		state.LoadBalancer.UpstreamServers = apiServers
		state.LoadBalancer.KonnectivityServerPort = r.konnectivityAgentPort
		state.pod.image = *r.envoyProxy.Image
		state.pod.pullPolicy = r.pullPolicy
	}

	go func() {
		defer close(watchDone)
		select {
		case <-time.After(60 * time.Second):
			r.log.Info("Starting to watch for Kubernetes API server address changes")
		case <-ctx.Done():
			return
		}

		wait.UntilWithContext(ctx, func(ctx context.Context) {
			if err := func() error {
				kubeClient, err := r.NewClient()
				if err != nil {
					return fmt.Errorf("failed to obtain load-balanced Kubernetes client: %w", err)
				}

				return watchEndpointsResource(ctx, r.log, kubeClient.CoreV1(), func(endpoints *corev1.Endpoints) error {
					return updateAPIServerAddresses(ctx, endpoints, updates, initialized.apiServersBackupFilePath)
				})
			}(); err != nil {
				r.log.WithError(err).Error("Failed to watch for Kubernetes API server address changes")
			}
		}, 10*time.Second)
	}()

	r.store(started)

	return nil
}

func (r *NllbReconciler) loadAPIServerAddressFromKubeconfig() (*hostPort, error) {
	kubeconfig, err := kubeutil.FirstExistingKubeconfig(r.kubeconfigPaths.regularPaths()...)()
	if err != nil {
		return nil, err
	}
	if len(kubeconfig.CurrentContext) < 1 {
		return nil, errors.New("current-context unspecified")
	}
	ctx, ok := kubeconfig.Contexts[kubeconfig.CurrentContext]
	if !ok {
		return nil, fmt.Errorf("current-context not found: %q", kubeconfig.CurrentContext)
	}
	cluster, ok := kubeconfig.Clusters[ctx.Cluster]
	if !ok {
		return nil, fmt.Errorf("cluster not found: %q", ctx.Cluster)
	}
	server, err := url.Parse(cluster.Server)
	if err != nil {
		return nil, fmt.Errorf("invalid server %q for cluster %q: %w", cluster.Server, ctx.Cluster, err)
	}

	var port uint16
	rawPort := server.Port()
	if rawPort == "" {
		switch server.Scheme {
		case "https":
			port = 443
		case "http":
			port = 80
		default:
			return nil, fmt.Errorf("unsupported URL scheme %q for server %q for cluster %q", server.Scheme, cluster.Server, ctx.Cluster)
		}
	} else {
		parsed, err := strconv.ParseUint(server.Port(), 10, 16)
		if err != nil {
			return nil, fmt.Errorf("invalid port %s for server %q for cluster %q: %w", rawPort, cluster.Server, ctx.Cluster, err)
		}
		port = uint16(parsed)
	}

	return &hostPort{server.Hostname(), port}, nil
}

type reconcilerStarted struct {
	*reconcilerConfig
	stop func()
}

func (*reconcilerStarted) String() string {
	return "started"
}

// podStateUpdateFunc updates the node-local load balancer pod state with with
// recent state changes.
type podStateUpdateFunc func(*podState)

func (r *NllbReconciler) reconcile(c *reconcilerConfig, updates <-chan podStateUpdateFunc) {
	var desiredState, actualConfigState, actualPodState podState
	var errors atomic.Bool

	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		lastRecoFailed := errors.Swap(false)

		select {
		case updateFunc, ok := <-updates:
			if !ok {
				return
			}

			updateFunc(&desiredState)

		case <-ticker.C: // Retry failed reconciliations every minute
			if !lastRecoFailed {
				continue
			}
		}

		var wg sync.WaitGroup

		wg.Add(1)
		go func() {
			defer wg.Done()

			if reflect.DeepEqual(desiredState.Shared, actualConfigState.Shared) && reflect.DeepEqual(desiredState.LoadBalancer, actualConfigState.LoadBalancer) {
				return
			}

			if err := r.updateLoadBalancerConfig(c, &desiredState); err != nil {
				errors.Store(true)
				r.log.WithError(err).Error("Failed to update load balancer configuration")
				return
			}

			desiredState.DeepCopyInto(&actualConfigState)
			r.log.Info("Load balancer configuration updated")
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()

			if reflect.DeepEqual(desiredState.Shared, actualPodState.Shared) && desiredState.pod == actualPodState.pod {
				return
			}

			if err := r.provision(c, &desiredState); err != nil {
				errors.Store(true)
				r.log.WithError(err).Error("Failed to update load balancer pod")
				return
			}

			desiredState.DeepCopyInto(&actualPodState)
			r.log.Info("Load balancer pod updated")
		}()

		wg.Wait()
	}
}

func (r *NllbReconciler) updateLoadBalancerConfig(c *reconcilerConfig, state *podState) error {
	return file.WriteAtomically(filepath.Join(c.envoyDir, "envoy.yaml"), 0444, func(file io.Writer) error {
		bufferedWriter := bufio.NewWriter(file)
		if err := envoyBootstrapConfig.Execute(bufferedWriter, state); err != nil {
			return fmt.Errorf("failed to generate configuration: %w", err)
		}
		return bufferedWriter.Flush()
	})
}

// admin:
//   address:
//     socket_address: { address: localhost, port_value: {{ .AdminPort }} }

var envoyBootstrapConfig = template.Must(template.New("Bootstrap").Parse(`
{{- $localKonnectivityPort := .Shared.KonnectivityAgentBindPort -}}
{{- $remoteKonnectivityPort := .LoadBalancer.KonnectivityServerPort -}}
static_resources:
  listeners:
  - name: apiserver
    address:
      socket_address: { address: {{ printf "%q" .Shared.LBAddr }}, port_value: {{ .Shared.APIServerBindPort }} }
    filter_chains:
    - filters:
      - name: envoy.filters.network.tcp_proxy
        typed_config:
          "@type": type.googleapis.com/envoy.extensions.filters.network.tcp_proxy.v3.TcpProxy
          stat_prefix: apiserver
          cluster: apiserver
  {{- if ne $localKonnectivityPort 0 }}
  - name: konnectivity
    address:
      socket_address: { address: {{ printf "%q" .Shared.LBAddr }}, port_value: {{ $localKonnectivityPort }} }
    filter_chains:
    - filters:
      - name: envoy.filters.network.tcp_proxy
        typed_config:
          "@type": type.googleapis.com/envoy.extensions.filters.network.tcp_proxy.v3.TcpProxy
          stat_prefix: konnectivity
          cluster: konnectivity
  {{- end }}
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
  {{- if ne $localKonnectivityPort 0 }}
  - name: konnectivity
    connect_timeout: 0.25s
    type: STATIC
    lb_policy: ROUND_ROBIN
    load_assignment:
      cluster_name: konnectivity
      endpoints:
      - lb_endpoints:
        {{- range .LoadBalancer.UpstreamServers }}
        - endpoint:
            address:
              socket_address:
                address: {{ printf "%q" .Host }}
                port_value: {{ $remoteKonnectivityPort }}
        {{- else }} []{{ end }}
    health_checks:
    # FIXME: What would be a proper health check?
    - tcp_health_check: {}
      timeout: 1s
      interval: 5s
      healthy_threshold: 3
      unhealthy_threshold: 5
  {{- end }}
`,
))

func (r *NllbReconciler) provision(c *reconcilerConfig, state *podState) error {
	manifest := corev1.Pod{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Pod"},
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
				Name:            "nllb",
				Image:           state.pod.image.URI(),
				ImagePullPolicy: state.pod.pullPolicy,
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
							Host: state.Shared.LBAddr.String(), Port: intstr.FromInt(int(state.Shared.APIServerBindPort)),
						},
					},
				},
			}},
			Volumes: []corev1.Volume{{
				Name: "envoy-config",
				VolumeSource: corev1.VolumeSource{
					HostPath: &corev1.HostPathVolumeSource{
						Path: c.envoyDir,
						Type: (*corev1.HostPathType)(pointer.String(string(corev1.HostPathDirectory))),
					},
				}},
			},
		},
	}

	if err := c.loadBalancerPod.SetManifest(manifest); err != nil {
		return err
	}

	r.log.Info("Provisioned static load balancer Pod")
	return nil
}

func (r *NllbReconciler) healthy(started *reconcilerStarted) error {
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

func (r *NllbReconciler) stop(c *reconcilerConfig) {
	defer r.store(&nllbStopped{})

	c.loadBalancerPod.Drop()

	for _, file := range []struct{ desc, path string }{
		{"envoy config", filepath.Join(c.envoyDir, "envoy.yaml")},
		{"bootstrap kubeconfig", r.kubeconfigPaths.bootstrap.loadBalanced},
		{"kubeconfig", r.kubeconfigPaths.regular.loadBalanced},
	} {
		if err := os.Remove(file.path); err != nil && !os.IsNotExist(err) {
			r.log.WithError(err).Warnf("Failed to remove %s from disk", file.desc)
		}
	}
}

type nllbStopped struct{}

func (*nllbStopped) String() string {
	return "stopped"
}

func getLoopbackIP() (net.IP, error) {
	loopbackIP := net.IP{127, 0, 0, 1}
	localIPs, err := net.LookupIP("localhost")
	if err != nil {
		err = fmt.Errorf("failed to resolve localhost: %w", err)
	} else {
		for _, ip := range localIPs {
			if ip.IsLoopback() {
				loopbackIP = ip
				break
			}
		}
	}

	return loopbackIP, err
}
