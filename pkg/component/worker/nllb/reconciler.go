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
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
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

const loadBalancerDefaultPort = 7443

// Reconciler reconciles a static Pod on a worker node that implements
// node-local load balancing.
type Reconciler struct {
	log logrus.FieldLogger

	envoyProxyImage v1beta1.ImageSpec
	pullPolicy      corev1.PullPolicy
	kubeconfigPaths kubeconfigPaths

	mu       sync.Mutex
	statePtr atomic.Pointer[reconcilerState]
}

var _ component.Component = (*Reconciler)(nil)
var _ component.Healthz = (*Reconciler)(nil)

// NewReconciler creates a component that reconciles a static Pod that
// implements node-local load balancing.
func NewReconciler(
	cfg *constant.CfgVars,
	staticPods worker.StaticPods,
	envoyProxyImage v1beta1.ImageSpec,
	pullPolicy corev1.PullPolicy,
) *Reconciler {
	dataDir := filepath.Join(cfg.DataDir, "nllb")
	r := &Reconciler{
		log: logrus.WithFields(logrus.Fields{"component": "node_local_load_balancer"}),

		envoyProxyImage: envoyProxyImage,
		pullPolicy:      pullPolicy,
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

	return r
}

func (r *Reconciler) GetKubeletKubeconfig() worker.KubeletKubeconfig {
	return worker.KubeletKubeconfig{
		Path:          r.kubeconfigPaths.regular.loadBalanced,
		BootstrapPath: r.kubeconfigPaths.bootstrap.regular,
	}
}

// NewClient returns a new Kubernetes client, backed by the node-local load balancer.
func (r *Reconciler) NewClient() (kubernetes.Interface, error) {
	return kubeutil.NewClient(kubeutil.FirstExistingKubeconfig(r.kubeconfigPaths.loadBalancedPaths()...))
}

func (r *Reconciler) Init(context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	switch state := r.state().(type) {
	case *reconcilerCreated:
		return r.init(state)
	default:
		return fmt.Errorf("node_local_load_balancer component is already %s", state)
	}
}

func (r *Reconciler) Start(context.Context) error {
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

func (r *Reconciler) Stop() error {
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
		return fmt.Errorf("node_local_load_balancer component is not yet started (%s)", state)
	}
}

func (r *Reconciler) Healthy() error {
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
		LBPort uint16
	}

	LoadBalancer struct {
		UpstreamServers []hostPort
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

func (r *Reconciler) state() reconcilerState {
	return *r.statePtr.Load()
}

func (r *Reconciler) store(state reconcilerState) {
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

func (r *Reconciler) init(created *reconcilerCreated) error {
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

func (r *Reconciler) start(initialized *reconcilerInitialized) error {
	apiServers := []hostPort{}
	apiServersBytes, err := os.ReadFile(initialized.apiServersBackupFilePath)
	if !os.IsNotExist(err) {
		if err != nil {
			return err
		}

		if err := yaml.Unmarshal(apiServersBytes, &apiServers); err != nil {
			return err
		}
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

		// FIXME use configurable address somehow?
		lbAddr := fmt.Sprintf("localhost:%d", loadBalancerDefaultPort)

		wait.UntilWithContext(ctx, func(ctx context.Context) {
			r.log.Info("Starting to reconcile kubeconfig files")
			if err := r.kubeconfigPaths.reconcile(ctx, r.log, lbAddr); err != nil {
				r.log.WithError(err).Error("Kubeconfig file reconciliation terminated erroneously")
			}

		}, 60*time.Second)
	}()

	updates <- func(state *podState) {
		// FIXME how to make this configurable?
		state.Shared.LBPort = loadBalancerDefaultPort
		state.LoadBalancer.UpstreamServers = apiServers
		state.pod.image = r.envoyProxyImage
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

				return watchEndpointsResource(ctx, kubeClient.CoreV1(), func(endpoints *corev1.Endpoints) error {
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

func (r *Reconciler) reconcile(c *reconcilerConfig, updates <-chan podStateUpdateFunc) {
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

			if desiredState.Shared == actualConfigState.Shared && reflect.DeepEqual(desiredState.LoadBalancer, actualConfigState.LoadBalancer) {
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

			if desiredState.Shared == actualPodState.Shared && desiredState.pod == actualPodState.pod {
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

func (r *Reconciler) updateLoadBalancerConfig(c *reconcilerConfig, state *podState) error {
	_, _, err := file.WriteAtomically(filepath.Join(c.envoyDir, "envoy.yaml"), 0444, func(file io.Writer) error {
		bufferedWriter := bufio.NewWriter(file)
		if err := envoyBootstrapConfig.Execute(bufferedWriter, state); err != nil {
			return fmt.Errorf("failed to generate configuration: %w", err)
		}
		return bufferedWriter.Flush()
	})

	return err
}

// admin:
//   address:
//     socket_address: { address: localhost, port_value: {{ .AdminPort }} }

var envoyBootstrapConfig = template.Must(template.New("Bootstrap").Parse(`
static_resources:
  listeners:
  - name: apiserver
    address:
      socket_address: { address: localhost, port_value: {{ .Shared.LBPort }} }
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

func (r *Reconciler) provision(c *reconcilerConfig, state *podState) error {
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
							Host: "127.0.0.1", Port: intstr.FromInt(int(state.Shared.LBPort)),
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

func (r *Reconciler) healthy(started *reconcilerStarted) error {
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

func (r *Reconciler) stop(c *reconcilerConfig) {
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
