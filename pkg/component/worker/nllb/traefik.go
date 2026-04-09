// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package nllb

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"

	"github.com/k0sproject/k0s/internal/pkg/dir"
	"github.com/k0sproject/k0s/internal/pkg/file"
	k0snet "github.com/k0sproject/k0s/internal/pkg/net"
	"github.com/k0sproject/k0s/pkg/apis/k0s/v1beta1"
	"github.com/k0sproject/k0s/pkg/applier"
	"github.com/k0sproject/k0s/pkg/component/worker"
	workerconfig "github.com/k0sproject/k0s/pkg/component/worker/config"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	"github.com/sirupsen/logrus"
	"sigs.k8s.io/yaml"
)

type traefikProxy struct {
	log logrus.FieldLogger

	dir        string
	staticPods worker.StaticPods

	pod    worker.StaticPod
	config *traefikConfig
}

var _ backend = (*traefikProxy)(nil)

type traefikPodParams struct {
	image         v1beta1.ImageSpec
	pullPolicy    corev1.PullPolicy
	hostConfigDir string
}

type traefikInstallConfig struct {
	bindIP                     net.IP
	apiServerBindPort          uint16
	konnectivityServerBindPort uint16
}

type traefikRoutingConfig struct {
	apiServers             []k0snet.HostPort
	konnectivityServerPort uint16
}

type traefikConfig struct {
	traefikPodParams
	traefikInstallConfig
	traefikRoutingConfig
}

const (
	traefikInstallConfigFileName = "config.yaml"
	traefikRoutingConfigFileName = "routing.yaml"
)

func (t *traefikProxy) init(ctx context.Context) error {
	if err := dir.InitWithOptions(t.dir).WithPermissions(0755).WithSELinuxLabel(containerFileLabel).Apply(); err != nil {
		return err
	}

	return nil
}

func (t *traefikProxy) start(ctx context.Context, profile workerconfig.Profile, apiServers []k0snet.HostPort) (err error) {
	if t.config != nil {
		return errors.New("already started")
	}

	t.pod, err = t.staticPods.ClaimStaticPod(metav1.NamespaceSystem, "nllb")
	if err != nil {
		t.pod = nil
		return fmt.Errorf("failed to claim static pod for TraefikProxy: %w", err)
	}

	defer func() {
		if err != nil {
			pod := t.pod
			pod.Clear()
			t.pod = nil
			t.stop()
			t.pod = pod
		}
	}()

	loopbackIP, err := getLoopbackIP(ctx)
	if err != nil {
		if errors.Is(err, ctx.Err()) {
			return err
		}
		t.log.WithError(err).Infof("Falling back to %s as bind address", loopbackIP)
	}

	nllb := profile.NodeLocalLoadBalancing
	var konnectivityBindPort uint16
	if nllb.TraefikProxy.KonnectivityServerBindPort != nil {
		konnectivityBindPort = uint16(*nllb.TraefikProxy.KonnectivityServerBindPort)
	}

	t.config = &traefikConfig{
		traefikPodParams: traefikPodParams{
			image:         *nllb.TraefikProxy.Image,
			pullPolicy:    nllb.TraefikProxy.ImagePullPolicy,
			hostConfigDir: t.dir,
		},
		traefikInstallConfig: traefikInstallConfig{
			bindIP:                     loopbackIP,
			apiServerBindPort:          uint16(nllb.TraefikProxy.APIServerBindPort),
			konnectivityServerBindPort: konnectivityBindPort,
		},
		traefikRoutingConfig: traefikRoutingConfig{
			apiServers:             apiServers,
			konnectivityServerPort: profile.Konnectivity.AgentPort,
		},
	}

	if err := t.writeInstallConfigFile(); err != nil {
		return err
	}
	if err := t.writeRoutingConfigFile(); err != nil {
		return err
	}

	return t.provision()
}

func (t *traefikProxy) getAPIServerAddress() (*k0snet.HostPort, error) {
	if t.config == nil {
		return nil, errors.New("not yet started")
	}
	return k0snet.NewHostPort(t.config.bindIP.String(), t.config.apiServerBindPort)
}

func (t *traefikProxy) updateAPIServers(apiServers []k0snet.HostPort) error {
	if t.config == nil {
		return errors.New("not yet started")
	}
	t.config.apiServers = apiServers
	return t.writeRoutingConfigFile()
}

func (t *traefikProxy) writeInstallConfigFile() error {
	content, err := t.config.traefikInstallConfig.toFileContent()
	if err != nil {
		return fmt.Errorf("failed to generate install configuration: %w", err)
	}
	return t.writeConfigFile(traefikInstallConfigFileName, content)
}

func (t *traefikProxy) writeRoutingConfigFile() error {
	content, err := t.config.traefikRoutingConfig.toFileContent(&t.config.traefikInstallConfig)
	if err != nil {
		return fmt.Errorf("failed to generate routing configuration: %w", err)
	}
	return t.writeConfigFile(traefikRoutingConfigFileName, content)
}

func (t *traefikProxy) stop() {
	if t.pod != nil {
		t.pod.Drop()
		t.pod = nil
	}

	if t.config == nil {
		return
	}

	for _, file := range []struct{ desc, name string }{
		{"Traefik install configuration", traefikInstallConfigFileName},
		{"Traefik routing configuration", traefikRoutingConfigFileName},
	} {
		path := filepath.Join(t.dir, file.name)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			t.log.WithError(err).Warnf("Failed to remove %s from disk", file.desc)
		}
	}

	t.config = nil
}

func (t *traefikProxy) writeConfigFile(name string, content []byte) error {
	return file.AtomicWithTarget(filepath.Join(t.dir, name)).
		WithPermissions(0444).
		WithSELinuxLabel(containerFileLabel).
		Write(content)
}

func (t *traefikProxy) provision() error {
	manifest := makeTraefikPodManifest(&t.config.traefikPodParams, &t.config.traefikInstallConfig)
	if err := t.pod.SetManifest(manifest); err != nil {
		return err
	}

	t.log.Info("Provisioned static Traefik Pod")
	return nil
}

func makeTraefikPodManifest(podParams *traefikPodParams, installConfig *traefikInstallConfig) corev1.Pod {
	ports := []corev1.ContainerPort{
		{Name: "api-server", ContainerPort: int32(installConfig.apiServerBindPort), Protocol: corev1.ProtocolTCP},
	}
	if installConfig.konnectivityServerBindPort != 0 {
		ports = append(ports, corev1.ContainerPort{Name: "konnectivity", ContainerPort: int32(installConfig.konnectivityServerBindPort), Protocol: corev1.ProtocolTCP})
	}

	// FIXME: Implement this properly in Traefik's config struct.
	//nolint:staticcheck // Ensure the image is copied by value
	var image v1beta1.ImageSpec = podParams.image
	if runtime.GOOS == "windows" {
		image.Version += "-windowsservercore"
	}

	pod := corev1.Pod{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Pod"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "nllb",
			Namespace: metav1.NamespaceSystem,
			Labels:    applier.CommonLabels("nllb"),
		},
		Spec: corev1.PodSpec{
			HostNetwork: true, // FIXME clarify if it's okay to set this for Windows, too.
			Containers: []corev1.Container{{
				Name:            "nllb",
				Image:           image.URI(),
				ImagePullPolicy: podParams.pullPolicy,
				Ports:           ports,
				Args:            []string{"--configFile=/etc/traefik/" + traefikInstallConfigFileName}, // FIXME windows paths?
				VolumeMounts: []corev1.VolumeMount{{
					Name:      "traefik-config",
					MountPath: "/etc/traefik",
					ReadOnly:  true,
				}},
				LivenessProbe: &corev1.Probe{
					PeriodSeconds:    10,
					FailureThreshold: 3,
					TimeoutSeconds:   3,
					ProbeHandler: corev1.ProbeHandler{
						TCPSocket: &corev1.TCPSocketAction{
							Host: installConfig.bindIP.String(), Port: intstr.FromInt(int(installConfig.apiServerBindPort)),
						},
					},
				},
			}},
			Volumes: []corev1.Volume{{
				Name: "traefik-config",
				VolumeSource: corev1.VolumeSource{
					HostPath: &corev1.HostPathVolumeSource{
						Path: podParams.hostConfigDir,
						Type: ptr.To(corev1.HostPathDirectory),
					},
				},
			}},
			Tolerations: []corev1.Toleration{{
				Operator: corev1.TolerationOpExists,
			}},
		},
	}

	if runtime.GOOS == "windows" {
		pod.Spec.SecurityContext = &corev1.PodSecurityContext{
			WindowsOptions: &corev1.WindowsSecurityContextOptions{
				HostProcess:   ptr.To(true),
				RunAsUserName: ptr.To(`NT AUTHORITY\system`), // FIXME: Do we need a special user?
			},
		}
		return pod
	}

	// FIXME: Eventually, we want to enforce this!
	// pod.Spec.SecurityContext = &corev1.PodSecurityContext{
	// 	RunAsNonRoot: ptr.To(true),
	// }
	pod.Spec.Containers[0].SecurityContext = &corev1.SecurityContext{
		ReadOnlyRootFilesystem:   ptr.To(true),
		Privileged:               ptr.To(false),
		AllowPrivilegeEscalation: ptr.To(false),
		Capabilities: &corev1.Capabilities{
			Drop: []corev1.Capability{"ALL"},
		},
	}

	return pod
}

func (i *traefikInstallConfig) toFileContent() ([]byte, error) {
	bindIP := i.bindIP.String()
	entryPoints := map[string]any{
		"apiserver": map[string]any{
			"address": net.JoinHostPort(bindIP, strconv.FormatUint(uint64(i.apiServerBindPort), 10)),
		},
	}
	if i.konnectivityServerBindPort != 0 {
		entryPoints["konnectivity"] = map[string]any{
			"address": net.JoinHostPort(bindIP, strconv.FormatUint(uint64(i.konnectivityServerBindPort), 10)),
		}
	}

	return yaml.Marshal(map[string]any{
		"global": map[string]any{
			"checkNewVersion":    false,
			"sendAnonymousUsage": false,
		},
		"log": map[string]any{
			"level":   "INFO",
			"noColor": true,
		},
		"entryPoints": entryPoints,
		"providers": map[string]any{
			"file": map[string]any{
				// FIXME windows paths?
				"filename": "/etc/traefik/" + traefikRoutingConfigFileName,
				"watch":    true,
			},
		},
	})
}

func (r *traefikRoutingConfig) toFileContent(installConfig *traefikInstallConfig) ([]byte, error) {
	const (
		apiserver    = "apiserver"
		konnectivity = "konnectivity"
	)

	routers := make(map[string]any)
	services := make(map[string]any)

	servers := make([]map[string]any, len(r.apiServers))
	for i := range r.apiServers {
		servers[i] = map[string]any{
			"address": r.apiServers[i].String(),
		}
	}

	routers[apiserver] = map[string]any{
		"entryPoints": []string{apiserver},
		"rule":        "HostSNI(`*`)",
		"service":     apiserver,
	}
	services[apiserver] = map[string]any{
		"loadBalancer": map[string]any{
			"servers": servers,
		},
	}

	if installConfig.konnectivityServerBindPort != 0 {
		port := strconv.FormatUint(uint64(r.konnectivityServerPort), 10)
		servers := make([]map[string]any, len(r.apiServers))
		for i := range r.apiServers {
			servers[i] = map[string]any{
				"address": net.JoinHostPort(r.apiServers[i].Host(), port),
			}
		}

		routers[konnectivity] = map[string]any{
			"entryPoints": []string{konnectivity},
			"rule":        "HostSNI(`*`)",
			"service":     konnectivity,
		}
		services[konnectivity] = map[string]any{
			"loadBalancer": map[string]any{
				"servers": servers,
			},
		}
	}

	return yaml.Marshal(map[string]any{
		"tcp": map[string]any{
			"routers":  routers,
			"services": services,
		},
	})
}
