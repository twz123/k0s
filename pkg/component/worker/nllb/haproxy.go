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
	"math/rand"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/k0sproject/k0s/internal/pkg/dir"
	"github.com/k0sproject/k0s/internal/pkg/file"
	k0snet "github.com/k0sproject/k0s/internal/pkg/net"
	"github.com/k0sproject/k0s/pkg/apis/k0s.k0sproject.io/v1beta1"
	"github.com/k0sproject/k0s/pkg/applier"
	"github.com/k0sproject/k0s/pkg/component/worker"
	workerconfig "github.com/k0sproject/k0s/pkg/component/worker/config"
	"go.uber.org/multierr"
	"golang.org/x/exp/slices"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/pointer"

	"github.com/sirupsen/logrus"
)

// haproxy is a load balancer [backend] that's managing a static HAProxy pod to
// implement node-local load balancing.
type haproxy struct {
	log logrus.FieldLogger

	rootDir    string
	staticPods worker.StaticPods

	runDir    string // The run directory for the haproxy pod.
	configDir string // Directory in which the haproxy config files are stored.
	reloadDir string // Directory on which the haproxy reloader will operate.

	pod    worker.StaticPod
	config *haproxyConfig
}

// var _ backend = (*haproxy)(nil)

// haproxyParams holds common parameters that are shared between all reconcilable parts.
type haproxyParams struct {
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

type batch []func() error

func (b *batch) Add(f func() error) { *b = append(*b, f) }

func (h *haproxy) init(ctx context.Context) error {
	h.runDir = filepath.Join(h.rootDir, "run")
	h.configDir = filepath.Join(h.rootDir, "config")
	h.reloadDir = filepath.Join(h.rootDir, "reload")

	if err := dir.Init(h.rootDir, 0700); err != nil {
		return err
	}
	// FIXME check perms
	if err := dir.Init(h.runDir, 0777); err != nil {
		return err
	}
	if err := dir.Init(h.configDir, 0755); err != nil {
		return err
	}
	if err := dir.Init(h.reloadDir, 0777); err != nil {
		return err
	}

	return nil
}

func (h *haproxy) start(ctx context.Context, profile workerconfig.Profile, apiServers []k0snet.HostPort) (err error) {
	if h.config != nil {
		return errors.New("already started")
	}

	h.pod, err = h.staticPods.ClaimStaticPod("kube-system", "nllb")
	if err != nil {
		h.pod = nil
		return fmt.Errorf("failed to claim static pod for HAProxy: %w", err)
	}

	defer func() {
		if err != nil {
			pod := h.pod
			pod.Clear()
			h.pod = nil
			h.stop()
			h.pod = pod
		}
	}()

	loopbackIP, err := getLoopbackIP(ctx)
	if err != nil {
		if errors.Is(err, ctx.Err()) {
			return err
		}
		h.log.WithError(err).Infof("Falling back to %s as bind address", loopbackIP)
	}

	nllb := profile.NodeLocalLoadBalancing
	h.config = &haproxyConfig{
		haproxyParams{
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
		h.config.haproxyFilesParams.konnectivityServerBindPort = uint16(*nllb.HAProxy.KonnectivityServerBindPort)
	}

	err = h.writeConfigFiles()
	if err != nil {
		return err
	}

	err = h.provision()
	if err != nil {
		return err
	}

	return nil
}

func (h *haproxy) getAPIServerAddress() (*k0snet.HostPort, error) {
	if h.config == nil {
		return nil, errors.New("not yet started")
	}
	return k0snet.NewHostPort(h.config.bindIP.String(), h.config.apiServerBindPort)
}

func (h *haproxy) updateAPIServers(ctx context.Context, apiServers []k0snet.HostPort) error {
	if h.config == nil {
		return errors.New("not yet started")
	}
	h.config.haproxyFilesParams.apiServers = apiServers

	var wg sync.WaitGroup

	var filesErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		filesErr = h.writeConfigFiles()
	}()

	var reconcileErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		reconcileErr = h.reconcileHAProxy(ctx, apiServers)
	}()

	wg.Wait()
	return multierr.Append(filesErr, reconcileErr)
}

func (h *haproxy) reconcileHAProxy(ctx context.Context, apiServers []k0snet.HostPort) error {
	var sender haproxySender = func(ctx context.Context, cmd []byte) (io.ReadCloser, error) {
		return sendToUnixSocket(ctx, filepath.Join(h.runDir, "haproxy.sock"), cmd)
	}

	servers, err := sender.DumpServers(ctx)
	if err != nil {
		return err
	}

	var batch batch

	upstreamAPIServers := servers["upstream_apiservers"]
	for _, server := range upstreamAPIServers {
		idx := slices.IndexFunc(apiServers, func(apiServer k0snet.HostPort) bool {
			return apiServer == server.address
		})
		if idx < 0 {
			batch.Add(func() error {
				server := "FIXME"
				err := sender.AddServer(ctx, "upstream_apiservers", server)
				if err != nil {
					return fmt.Errorf("while reconciling upstream_apiservers/%s: %w", server, err)
				}
				return nil
			})
		}
	}

	upstreamKonnectivityServers := servers["upstream_konnectivity_servers"]

	// Try to delete any unknown servers.
	delete(servers, "upstream_apiservers")
	delete(servers, "upstream_konnectivity_servers")
	for backend, servers := range servers {
		for _, server := range servers {
			if err := ctx.Err(); err != nil {
				err := fmt.Errorf("%w while reconciling servers", err)
				return multierr.Append(err, errs)
			}

			err := sender.DeleteServer(ctx, backend, server.name)
			if err != nil {
				err := fmt.Errorf("while reconciling %s/%s: %w", backend, server, err)
				errs = multierr.Append(errs, err)
			}
		}
	}

	return errs
}

func (h *haproxy) reconcileAPIServers(apiServers []k0snet.HostPort) error {

	if h.config == nil {
		return errors.New("not yet started")
	}
	h.config.haproxyFilesParams.apiServers = apiServers

	return h.writeConfigFiles()

}

func (h *haproxy) stop() {
	if h.pod != nil {
		h.pod.Drop()
		h.pod = nil
	}

	if h.config == nil {
		return
	}

	for _, file := range []struct{ desc, name string }{
		{"HAProxy config", haproxyCfgFile},
		{"HAProxy reload marker", haproxyReloadMarkerFile},
	} {
		path := filepath.Join(h.configDir, file.name)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			h.log.WithError(err).Warnf("Failed to remove %s from disk", file.desc)
		}
	}

	h.config = nil
}

func (h *haproxy) writeConfigFiles() (err error) {
	data := struct {
		APIServerBindAddress                *k0snet.HostPort
		APIServerUpstreamAddresses          []k0snet.HostPort
		KonnectivityServerBindAddress       *k0snet.HostPort
		KonnectivityServerUpstreamAddresses []k0snet.HostPort
	}{}

	data.APIServerBindAddress, err = k0snet.NewHostPort(h.config.bindIP.String(), h.config.apiServerBindPort)
	if err != nil {
		return err
	}
	data.APIServerUpstreamAddresses = h.config.apiServers

	if h.config.konnectivityServerBindPort != 0 {
		data.KonnectivityServerBindAddress, err = k0snet.NewHostPort(h.config.bindIP.String(), h.config.konnectivityServerBindPort)
		if err != nil {
			return err
		}
		for _, apiServer := range h.config.apiServers {
			server, err := k0snet.NewHostPort(apiServer.Host(), h.config.konnectivityServerPort)
			if err != nil {
				return err
			}
			data.KonnectivityServerUpstreamAddresses = append(data.KonnectivityServerUpstreamAddresses, *server)
		}
	}

	if err := file.WriteAtomically(filepath.Join(h.configDir, haproxyCfgFile), 0444, func(file io.Writer) error {
		bufferedWriter := bufio.NewWriter(file)
		if err := haproxyCfgTemplate.Execute(bufferedWriter, data); err != nil {
			return fmt.Errorf("failed to render template: %w", err)
		}
		return bufferedWriter.Flush()
	}); err != nil {
		return err
	}

	reloadMarkerPath := filepath.Join(h.reloadDir, haproxyReloadMarkerFile)
	if err := file.WriteContentAtomically(reloadMarkerPath, []byte{}, 0666); err != nil {
		return err
	}

	return nil
}

func (h *haproxy) provision() error {
	if err := h.pod.SetManifest(h.makePodManifest()); err != nil {
		return err
	}

	h.log.Info("Provisioned static HAProxy Pod")
	return nil
}

func (h *haproxy) makePodManifest() corev1.Pod {
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
				Image:           h.config.image.URI(),
				ImagePullPolicy: h.config.pullPolicy,
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
							Host: h.config.bindIP.String(), Port: intstr.FromInt(int(h.config.apiServerBindPort)),
						},
					},
				},
			}, {
				Name:            "reloader",
				Image:           h.config.image.URI(),
				ImagePullPolicy: h.config.pullPolicy,
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
					HostPath: &corev1.HostPathVolumeSource{
						Type: &hostPathDir,
						Path: h.runDir,
					},
				},
			}, {
				Name: "config",
				VolumeSource: corev1.VolumeSource{
					HostPath: &corev1.HostPathVolumeSource{
						Type: &hostPathDir,
						Path: h.configDir,
					},
				},
			}, {
				Name: "reloader",
				VolumeSource: corev1.VolumeSource{
					HostPath: &corev1.HostPathVolumeSource{
						Type: &hostPathDir,
						Path: h.reloadDir,
					},
				},
			}},
		},
	}
}

func xoxo() {
	const letterBytes = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	// Seed the random number generator
	rand.Seed(time.Now().UnixNano())

	// Generate a random string of 8 characters
	b := make([]byte, 8)
	for i := range b {
		b[i] = letterBytes[rand.Intn(len(letterBytes))]
	}
	s := string(b)
	fmt.Println(s)
}

type haproxySender func(ctx context.Context, cmd []byte) (io.ReadCloser, error)

// HAProxy server addresses keyed by their name.
type haproxyServers = map[string]k0snet.HostPort

// Dump all the the servers found in the running configuration, grouped by backends.
func (sender haproxySender) DumpServers(ctx context.Context) (map[string]haproxyServers, error) {
	// https://docs.haproxy.org/2.7/management.html#9.3-show%20servers%20state
	lines, err := sender.sendCommand(ctx, "show servers state")
	if err != nil {
		return nil, err
	}

	// The dump has the following format:
	// - first line contains the format version (1 in this specification);
	// - second line contains the column headers, prefixed by a sharp ('#');
	// - third line and next ones contain data;
	// - each line starting by a sharp ('#') is considered as a comment.

	if len(lines) < 1 {
		return nil, errors.New("empty response")
	}

	if lines[0] != "1" {
		return nil, fmt.Errorf("invalid format version: %s", lines[0])
	}
	if len(lines) < 2 {
		return nil, errors.New("no column headers")
	}
	headers := strings.Split(lines[1], " ")
	if len(headers) < 1 || headers[0] != "#" {
		return nil, fmt.Errorf("invalid column headers: %s", lines[1])
	}
	headers = headers[1:] // strip hashbang
	beNameIdx := slices.Index(headers, "be_name")
	if beNameIdx < 0 {
		return nil, fmt.Errorf("no be_name column: %s", lines[1])
	}
	srvNameIdx := slices.Index(headers, "srv_name")
	if srvNameIdx < 0 {
		return nil, fmt.Errorf("no srv_name column: %s", lines[1])
	}
	srvAddrIdx := slices.Index(headers, "srv_addr")
	if srvAddrIdx < 0 {
		return nil, fmt.Errorf("no srv_addr column: %s", lines[1])
	}
	srvPortIdx := slices.Index(headers, "srv_port")
	if srvPortIdx < 0 {
		return nil, fmt.Errorf("no srv_port column: %s", lines[1])
	}

	servers := make(map[string]haproxyServers)
	for _, line := range lines[2:] {
		if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
			continue
		}

		fields := strings.Split(line, " ")
		if len(fields) != len(headers) {
			return nil, fmt.Errorf("mismatch between headers and fields: %v vs. %v", headers, fields)
		}

		addr, err := k0snet.ParseHostAndPort(fields[srvAddrIdx], fields[srvPortIdx])
		if err != nil {
			return nil, fmt.Errorf("failed to parse server %v: %w", fields, err)
		}

		backendName, serverName := fields[beNameIdx], fields[srvNameIdx]

		backendServers := servers[backendName]
		if backendServers == nil {
			backendServers = make(haproxyServers)
			servers[backendName] = backendServers
		}

		backendServers[serverName] = *addr
	}

	return servers, nil
}

func (sender haproxySender) DeleteServer(ctx context.Context, backend, server string) (errs error) {
	// The server must be put in maintenance mode prior to its deletion.
	// https://docs.haproxy.org/2.7/management.html#9.3-set%20server
	response, err := sender.sendCommand(ctx, fmt.Sprintf("set server %s/%s state maint", backend, server))
	if err == nil && !slices.Equal(response, []string{"", ""}) {
		err = fmt.Errorf("unexpected response: %v", response)
	}
	if err != nil {
		err = fmt.Errorf("failed to put server in maintenance mode: %w", err)
		errs = multierr.Append(errs, err)
	}

	// https://docs.haproxy.org/2.7/management.html#9.3-del%20server
	// NB: The delete operation is cancelled if the serveur still has active or
	// idle connection or its connection queue is not empty.
	response, err = sender.sendCommand(ctx, fmt.Sprintf("del server %s/%s", backend, server))
	if err == nil && !slices.Equal(response, []string{"", ""}) {
		err = fmt.Errorf("unexpected response: %v", response)
	}
	if err != nil {
		err = fmt.Errorf("failed to delete server: %w", err)
		errs = multierr.Append(errs, err)
	}

	return errs
}

func (sender haproxySender) sendCommand(ctx context.Context, cmd string) (lines []string, err error) {
	response, err := sender(ctx, []byte(cmd+"\n"))
	if err != nil {
		return nil, err
	}
	defer func() { err = multierr.Append(err, response.Close()) }()

	scanner := bufio.NewScanner(bufio.NewReader(response))
	scanner.Split(bufio.ScanLines)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	if len(lines) > 0 && strings.HasPrefix(lines[0], "Unknown command:") {
		return nil, errors.New("unknown command")
	}

	return lines, nil
}

func sendToUnixSocket(ctx context.Context, path string, cmd []byte) (conn net.Conn, err error) {
	address := url.URL{Scheme: "unix", Path: path}

	var dialer net.Dialer
	conn, err = dialer.DialContext(ctx, "unix", address.String())
	if err != nil {
		return nil, err
	}

	close := true
	defer func() {
		if close {
			err = multierr.Append(err, conn.Close())
		}
	}()

	_, err = conn.Write(cmd)
	if err != nil {
		return nil, err
	}

	close = false
	return conn, nil
}

var haproxyCfgTemplate = template.Must(template.New(haproxyCfgFile).Parse(`
global
  pidfile /run/nllb/haproxy.pid
  stats socket /run/nllb/haproxy.sock mode 600 level admin

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
