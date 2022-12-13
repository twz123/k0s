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
	"os"
	"path/filepath"
	"text/template"

	"github.com/k0sproject/k0s/internal/pkg/dir"
	"github.com/k0sproject/k0s/internal/pkg/file"
	k0snet "github.com/k0sproject/k0s/internal/pkg/net"
	"github.com/k0sproject/k0s/pkg/apis/k0s.k0sproject.io/v1beta1"
	"github.com/k0sproject/k0s/pkg/applier"
	"github.com/k0sproject/k0s/pkg/component/worker"
	workerconfig "github.com/k0sproject/k0s/pkg/component/worker/config"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/pointer"

	"github.com/sirupsen/logrus"
)

// haproxy is a load balancer [backend] that's managing a static HAProxy pod to
// implement node-local load balancing.
type haproxy struct {
	log logrus.FieldLogger

	dir        string
	staticPods worker.StaticPods

	pod    worker.StaticPod
	config *haproxyConfig
}

var _ backend = (*haproxy)(nil)

// haproxyParams holds common parameters that are shared between all reconcilable parts.
type haproxyParams struct {
	// Directory in which the haproxy config files are stored.
	configDir string

	// Directory on which the haproxy reloader will operate.
	reloadDir string

	// IP to which HAProxy will bind.
	bindIP net.IP

	// Port to which HAProxy will bind the API server load balancer.
	apiServerBindPort uint16
}

// haproxyPodParams holds the parameters for the static HAProxy pod template.
type haproxyPodParams struct {
	// The HAProxy image to pull.
	image v1beta1.ImageSpec

	// The pull policy to use for the HAProxy container.
	pullPolicy corev1.PullPolicy
}

// haproxyFilesParams holds the parameters for the HAProxy config files.
type haproxyFilesParams struct {
	// Port to which HAProxy will bind the konnectivity server load balancer.
	konnectivityServerBindPort uint16

	// Addresses on which the upstream API servers are listening.
	apiServers []k0snet.HostPort

	// Port on which the upstream konnectivity servers are listening.
	konnectivityServerPort uint16
}

// haproxyConfig is a convenience struct that combines all haproxy parameters.
type haproxyConfig struct {
	haproxyParams
	haproxyPodParams
	haproxyFilesParams
}

const (
	haproxyCfgFile          = "haproxy.cfg"
	haproxyReloadMarkerFile = "reload"
)

func (e *haproxy) init(ctx context.Context) error {
	if err := dir.Init(e.dir, 0755); err != nil {
		return err
	}
	if err := dir.Init(filepath.Join(e.dir, "config"), 0755); err != nil {
		return err
	}
	if err := dir.Init(filepath.Join(e.dir, "reload"), 0777); err != nil {
		return err
	}

	return nil
}

func (e *haproxy) start(ctx context.Context, profile workerconfig.Profile, apiServers []k0snet.HostPort) (err error) {
	if e.config != nil {
		return errors.New("already started")
	}

	e.pod, err = e.staticPods.ClaimStaticPod("kube-system", "nllb")
	if err != nil {
		e.pod = nil
		return fmt.Errorf("failed to claim static pod for HAProxy: %w", err)
	}

	defer func() {
		if err != nil {
			pod := e.pod
			pod.Clear()
			e.pod = nil
			e.stop()
			e.pod = pod
		}
	}()

	loopbackIP, err := getLoopbackIP(ctx)
	if err != nil {
		if errors.Is(err, ctx.Err()) {
			return err
		}
		e.log.WithError(err).Infof("Falling back to %s as bind address", loopbackIP)
	}

	nllb := profile.NodeLocalLoadBalancing
	e.config = &haproxyConfig{
		haproxyParams{
			filepath.Join(e.dir, "config"),
			filepath.Join(e.dir, "reload"),
			loopbackIP,
			uint16(profile.NodeLocalLoadBalancing.HAProxy.APIServerBindPort),
		},
		haproxyPodParams{
			*nllb.HAProxy.Image,
			nllb.HAProxy.ImagePullPolicy,
		},
		haproxyFilesParams{
			konnectivityServerPort: profile.Konnectivity.AgentPort,
			apiServers:             apiServers,
		},
	}

	if nllb.HAProxy.KonnectivityServerBindPort != nil {
		e.config.haproxyFilesParams.konnectivityServerBindPort = uint16(*nllb.HAProxy.KonnectivityServerBindPort)
	}

	err = writeHAProxyConfigFiles(&e.config.haproxyParams, &e.config.haproxyFilesParams)
	if err != nil {
		return err
	}

	err = e.provision()
	if err != nil {
		return err
	}

	return nil
}

func (e *haproxy) getAPIServerAddress() (*k0snet.HostPort, error) {
	if e.config == nil {
		return nil, errors.New("not yet started")
	}
	return k0snet.NewHostPort(e.config.bindIP.String(), e.config.apiServerBindPort)
}

func (e *haproxy) updateAPIServers(apiServers []k0snet.HostPort) error {
	if e.config == nil {
		return errors.New("not yet started")
	}
	e.config.haproxyFilesParams.apiServers = apiServers
	return writeHAProxyConfigFiles(&e.config.haproxyParams, &e.config.haproxyFilesParams)
}

func (e *haproxy) stop() {
	if e.pod != nil {
		e.pod.Drop()
		e.pod = nil
	}

	if e.config == nil {
		return
	}

	for _, file := range []struct{ desc, name string }{
		{"HAProxy config", haproxyCfgFile},
		{"HAProxy reload marker", haproxyReloadMarkerFile},
	} {
		path := filepath.Join(e.config.configDir, file.name)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			e.log.WithError(err).Warnf("Failed to remove %s from disk", file.desc)
		}
	}

	e.config = nil
}

func writeHAProxyConfigFiles(params *haproxyParams, filesParams *haproxyFilesParams) (err error) {
	data := struct {
		APIServerBindAddress                *k0snet.HostPort
		APIServerUpstreamAddresses          []k0snet.HostPort
		KonnectivityServerBindAddress       *k0snet.HostPort
		KonnectivityServerUpstreamAddresses []k0snet.HostPort
	}{}

	data.APIServerBindAddress, err = k0snet.NewHostPort(params.bindIP.String(), params.apiServerBindPort)
	if err != nil {
		return err
	}
	data.APIServerUpstreamAddresses = filesParams.apiServers

	if filesParams.konnectivityServerBindPort != 0 {
		data.KonnectivityServerBindAddress, err = k0snet.NewHostPort(params.bindIP.String(), filesParams.konnectivityServerBindPort)
		if err != nil {
			return err
		}
		for _, apiServer := range filesParams.apiServers {
			server, err := k0snet.NewHostPort(apiServer.Host(), filesParams.konnectivityServerPort)
			if err != nil {
				return err
			}
			data.KonnectivityServerUpstreamAddresses = append(data.KonnectivityServerUpstreamAddresses, *server)
		}
	}

	if err := file.WriteAtomically(filepath.Join(params.configDir, haproxyCfgFile), 0444, func(file io.Writer) error {
		bufferedWriter := bufio.NewWriter(file)
		if err := haproxyCfgTemplate.Execute(bufferedWriter, data); err != nil {
			return fmt.Errorf("failed to render template: %w", err)
		}
		return bufferedWriter.Flush()
	}); err != nil {
		return err
	}

	reloadMarkerPath := filepath.Join(params.reloadDir, haproxyReloadMarkerFile)
	if err := file.WriteContentAtomically(reloadMarkerPath, []byte{}, 0666); err != nil {
		return err
	}

	return nil
}

func (e *haproxy) provision() error {
	manifest := makeHAProxyPodManifest(&e.config.haproxyParams, &e.config.haproxyPodParams)
	if err := e.pod.SetManifest(manifest); err != nil {
		return err
	}

	e.log.Info("Provisioned static HAProxy Pod")
	return nil
}

func makeHAProxyPodManifest(params *haproxyParams, podParams *haproxyPodParams) corev1.Pod {
	const reloader = `while :; do
sleep 1
if [ -e reload ]; then
	rm -f -- reload
	kill -s SIGUSR2 $(cat /run/nllb/haproxy.pid)
fi
done
`

	hostPathDir := corev1.HostPathDirectory
	noCapabilities := &corev1.SecurityContext{
		ReadOnlyRootFilesystem:   pointer.Bool(true),
		Privileged:               pointer.Bool(false),
		AllowPrivilegeEscalation: pointer.Bool(false),
		Capabilities: &corev1.Capabilities{
			Drop: []corev1.Capability{"ALL"},
		},
	}

	return corev1.Pod{
		TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Pod"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "nllb",
			Namespace: "kube-system",
			Labels:    applier.CommonLabels("nllb"),
		},
		Spec: corev1.PodSpec{
			HostNetwork: true,
			// The official HAProxy image uses non-numeric users
			// SecurityContext: &corev1.PodSecurityContext{
			// 	RunAsNonRoot: pointer.Bool(true),
			// },
			ShareProcessNamespace: pointer.Bool(true),
			Containers: []corev1.Container{{
				Name:            "nllb",
				Image:           podParams.image.URI(),
				ImagePullPolicy: podParams.pullPolicy,
				Ports: []corev1.ContainerPort{
					{Name: "nllb", ContainerPort: 80, Protocol: corev1.ProtocolTCP},
				},
				SecurityContext: noCapabilities,
				VolumeMounts: []corev1.VolumeMount{{
					Name:      "config",
					MountPath: "/usr/local/etc/haproxy",
					ReadOnly:  true,
				}, {
					Name:      "run",
					MountPath: "/run/nllb",
				}},
				LivenessProbe: &corev1.Probe{
					PeriodSeconds:    10,
					FailureThreshold: 3,
					TimeoutSeconds:   3,
					ProbeHandler: corev1.ProbeHandler{
						TCPSocket: &corev1.TCPSocketAction{
							Host: params.bindIP.String(), Port: intstr.FromInt(int(params.apiServerBindPort)),
						},
					},
				},
			}, {
				Name:            "reloader",
				Image:           podParams.image.URI(),
				ImagePullPolicy: podParams.pullPolicy,
				SecurityContext: noCapabilities,
				Command:         []string{"sh", "-ec", reloader},
				WorkingDir:      "/reloader",
				VolumeMounts: []corev1.VolumeMount{{
					Name:      "reloader",
					MountPath: "/reloader",
				}, {
					Name:      "run",
					MountPath: "/run/nllb",
					ReadOnly:  true,
				}},
			}},
			Volumes: []corev1.Volume{{
				Name: "run",
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{
						Medium:    corev1.StorageMediumMemory,
						SizeLimit: resource.NewQuantity(1024*1024, resource.BinarySI),
					},
				},
			}, {
				Name: "config",
				VolumeSource: corev1.VolumeSource{
					HostPath: &corev1.HostPathVolumeSource{
						Type: &hostPathDir,
						Path: params.configDir,
					},
				},
			}, {
				Name: "reloader",
				VolumeSource: corev1.VolumeSource{
					HostPath: &corev1.HostPathVolumeSource{
						Type: &hostPathDir,
						Path: params.reloadDir,
					},
				},
			}},
		},
	}
}

var haproxyCfgTemplate = template.Must(template.New(haproxyCfgFile).Parse(`
global
  pidfile /run/nllb/haproxy.pid

defaults
  mode tcp
  timeout connect 10s
  timeout server 60s
  timeout client 60s

frontend node_local_apiserver
  bind {{ .APIServerBindAddress }}
  default_backend upstream_apiservers

backend upstream_apiservers
  balance random
  {{- range $i, $addr := .APIServerUpstreamAddresses }}
  server apiserver-{{ $i }} {{ $addr }} check
  {{- end }}

{{ if .KonnectivityServerBindAddress }}
frontend node_local_konnectivity_servers
  bind {{ .KonnectivityServerBindAddress }}
  default_backend upstream_konnectivity_servers

backend upstream_konnectivity_servers
  balance roundrobin
  {{- range $i, $addr := .KonnectivityServerUpstreamAddresses }}
  server konnectivity-server-{{ $i }} {{ $addr }}
  {{- end }}
{{- end }}
`))
