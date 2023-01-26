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
	"strconv"
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
	"k8s.io/apiserver/pkg/storage/names"
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

	pod    worker.StaticPod
	config *haproxyConfig

	reconcileMu sync.Mutex
}

var _ backend = (*haproxy)(nil)

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
	haproxyCfgFile = "haproxy.cfg"
)

type batch []func() error

func (b *batch) Add(f func() error) { *b = append(*b, f) }

func (h *haproxy) init(ctx context.Context) error {
	h.runDir = filepath.Join(h.rootDir, "run")
	h.configDir = filepath.Join(h.rootDir, "config")

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
		for {
			var ok bool
			ok, reconcileErr = h.reconcileHAProxy(ctx, apiServers)
			if reconcileErr == nil && !ok {
				select {
				case <-ctx.Done():
					reconcileErr = ctx.Err()
				case <-time.After(1 * time.Second):
					continue
				}
			}
			return
		}
	}()

	wg.Wait()
	return multierr.Append(filesErr, reconcileErr)
}

func (h *haproxy) reconcileHAProxy(ctx context.Context, apiServers []k0snet.HostPort) (bool, error) {
	sender := haproxySender{h.log, func(ctx context.Context, cmd []byte) (io.ReadCloser, error) {
		return sendToUNIXSocket(ctx, filepath.Join(h.runDir, "haproxy.sock"), cmd)
	}}

	servers, err := sender.ShowServersState(ctx)
	if err != nil {
		return false, err
	}

	var batch batch

	// Calculate changes to API server backend
	addrsToAdd := slices.Clone(apiServers)
	for server, state := range servers["upstream_apiservers"] {
		server := server

		idx := slices.IndexFunc(addrsToAdd, func(candidate k0snet.HostPort) bool {
			return candidate == state.addr
		})

		if idx >= 0 {
			upToDate := true

			if !state.checkEnabled {
				upToDate = false
				h.log.Debugf("Enabling health checks for API server address %s", &state.addr)
				batch.Add(func() error {
					err := sender.EnableHealth(ctx, "upstream_apiservers", server)
					if err != nil {
						return fmt.Errorf("while enabling health checks for upstream_apiservers/%s: %w", server, err)
					}
					return nil
				})
			}

			if state.maint {
				upToDate = false
				h.log.Debugf("Enabling API server address %s", &state.addr)
				batch.Add(func() error {
					err := sender.SetServerState(ctx, "upstream_apiservers", server, "ready")
					if err != nil {
						return fmt.Errorf("while putting upstream_apiservers/%s into ready state: %w", server, err)
					}
					return nil
				})
			}

			if upToDate {
				h.log.Debugf("API server address %s up to date", &state.addr)
			}

			addrsToAdd = slices.Delete(addrsToAdd, idx, idx+1)
			continue
		}

		h.log.Debugf("Removing API server address %s", &state.addr)
		if !state.maint {
			batch.Add(func() error {
				// The server must be put in maintenance mode prior to its deletion.
				err := sender.SetServerState(ctx, "upstream_apiservers", server, "maint")
				if err != nil {
					return fmt.Errorf("while putting upstream_apiservers/%s into maintenance state: %w", server, err)
				}
				return nil
			})
		}

		batch.Add(func() error {
			_, err := sender.DeleteServer(ctx, "upstream_apiservers", server)
			if err != nil {
				return fmt.Errorf("while deleting upstream_apiservers/%s: %w", server, err)
			}
			return nil
		})
	}
	for _, addr := range addrsToAdd {
		h.log.Debugf("Adding API server address %s", &addr)
		server := names.SimpleNameGenerator.GenerateName("apiserver-")
		batch.Add(func() error {
			err := sender.AddServer(ctx, "upstream_apiservers", server, addr.String(), "check")
			if err != nil {
				return fmt.Errorf("while adding upstream_apiservers/%s: %w", server, err)
			}
			return nil
		})
		batch.Add(func() error {
			err := sender.SetServerState(ctx, "upstream_apiservers", server, "ready")
			if err != nil {
				return fmt.Errorf("while putting upstream_apiservers/%s into ready state: %w", server, err)
			}
			return nil
		})
		batch.Add(func() error {
			err := sender.EnableHealth(ctx, "upstream_apiservers", server)
			if err != nil {
				return fmt.Errorf("while enabling health checks for upstream_apiservers/%s into ready state: %w", server, err)
			}
			return nil
		})
	}

	// Calculate changes to Konnectivity backend
	addrsToAdd = slices.Clone(apiServers)
	for server, state := range servers["upstream_konnectivity_servers"] {
		if state.addr.Port() == h.config.konnectivityServerPort {
			server := server

			idx := slices.IndexFunc(addrsToAdd, func(apiServer k0snet.HostPort) bool {
				return apiServer.Host() == state.addr.Host()
			})

			if idx >= 0 {
				if state.maint {
					h.log.Debugf("Enabling Konnectivity server address %s", &state.addr)
					batch.Add(func() error {
						err := sender.SetServerState(ctx, "upstream_konnectivity_servers", server, "ready")
						if err != nil {
							return fmt.Errorf("while putting upstream_konnectivity_servers/%s into ready state: %w", server, err)
						}
						return nil
					})
				} else {
					h.log.Debugf("Konnectivity server address %s up to date", &state.addr)
				}

				addrsToAdd = slices.Delete(addrsToAdd, idx, idx+1)
				continue
			}
		}

		h.log.Debugf("Removing Konnectivity server address %s", &state.addr)
		server := server

		if !state.maint {
			batch.Add(func() error {
				// The server must be put in maintenance mode prior to its deletion.
				err := sender.SetServerState(ctx, "upstream_konnectivity_servers", server, "maint")
				if err != nil {
					return fmt.Errorf("while putting upstream_konnectivity_servers/%s into maintenance state: %w", server, err)
				}
				return nil
			})
		}

		batch.Add(func() error {
			_, err := sender.DeleteServer(ctx, "upstream_konnectivity_servers", server)
			if err != nil {
				return fmt.Errorf("while deleting upstream_konnectivity_servers/%s: %w", server, err)
			}
			return nil
		})
	}
	for _, addr := range addrsToAdd {
		konnectivityPort := strconv.FormatUint(uint64(h.config.konnectivityServerPort), 10)
		konnectivityAddr := net.JoinHostPort(addr.Host(), konnectivityPort)

		h.log.Debugf("Adding Konnectivity server address %s", konnectivityAddr)
		server := names.SimpleNameGenerator.GenerateName("konnectivity-server-")
		batch.Add(func() error {
			err := sender.AddServer(ctx, "upstream_konnectivity_servers", server, konnectivityAddr)
			if err != nil {
				return fmt.Errorf("while adding upstream_konnectivity_servers/%s: %w", server, err)
			}
			return nil
		})
		batch.Add(func() error {
			err := sender.SetServerState(ctx, "upstream_konnectivity_servers", server, "ready")
			if err != nil {
				return fmt.Errorf("while putting upstream_konnectivity_servers/%s into ready state: %w", server, err)
			}
			return nil
		})
	}

	// Try to delete any unknown servers.
	delete(servers, "upstream_apiservers")
	delete(servers, "upstream_konnectivity_servers")
	for backend, servers := range servers {
		for server := range servers {
			backend, server := backend, server
			batch.Add(func() error {
				_, err := sender.DeleteServer(ctx, backend, server)
				if err != nil {
					return fmt.Errorf("while deleting %s/%s: %w", backend, server, err)
				}
				return nil
			})
		}
	}

	if len(batch) < 1 {
		return true, nil
	}

	var errs error
	for _, op := range batch {
		errs = multierr.Combine(errs, op())
		if err := ctx.Err(); err != nil {
			if !errors.Is(errs, err) {
				errs = multierr.Combine(err, errs)
			}

			break
		}
	}

	return false, errs
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
			}},
		},
	}
}

type haproxySender struct {
	log  logrus.FieldLogger
	send func(ctx context.Context, cmd []byte) (io.ReadCloser, error)
}

// HAProxy server addresses keyed by their name.
type haproxyServers = map[string]serverState

type serverState struct {
	addr         k0snet.HostPort
	maint        bool
	checkEnabled bool
}

// Dump all the the servers found in the running configuration, grouped by backends.
func (s *haproxySender) ShowServersState(ctx context.Context) (map[string]haproxyServers, error) {
	//revive:disable:var-naming
	const (
		SRV_ADMF_FMAINT = 0x01 // The server was explicitly forced into maintenance.
		CHK_ST_ENABLED  = 0x04 // This check is currently administratively enabled.
	)
	//revive:enable:var-naming

	// https://docs.haproxy.org/2.7/management.html#9.3-show%20servers%20state
	lines, err := s.sendCommand(ctx, "show servers state")
	if err != nil {
		return nil, err
	}

	s.log.Debug(strings.Join(lines, "\n"))

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
	srvAdminStateIdx := slices.Index(headers, "srv_admin_state")
	if srvPortIdx < 0 {
		return nil, fmt.Errorf("no srv_admin_state column: %s", lines[1])
	}
	srvCheckStateIdx := slices.Index(headers, "srv_check_state")
	if srvPortIdx < 0 {
		return nil, fmt.Errorf("no srv_check_state column: %s", lines[1])
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
		srvAdminState, err := strconv.ParseUint(fields[srvAdminStateIdx], 10, 32)
		if err != nil {
			return nil, fmt.Errorf("failed to parse server %v: %w", fields, err)
		}
		srvCheckState, err := strconv.ParseUint(fields[srvCheckStateIdx], 10, 32)
		if err != nil {
			return nil, fmt.Errorf("failed to parse server %v: %w", fields, err)
		}

		backendName, serverName := fields[beNameIdx], fields[srvNameIdx]

		backendServers := servers[backendName]
		if backendServers == nil {
			backendServers = make(haproxyServers)
			servers[backendName] = backendServers
		}

		backendServers[serverName] = serverState{
			*addr,
			srvAdminState&SRV_ADMF_FMAINT != 0,
			srvCheckState&CHK_ST_ENABLED != 0,
		}
	}

	s.log.Debug(servers)
	return servers, nil
}

// Instantiate a new server attached to the backend.
//
// https://docs.haproxy.org/2.7/management.html#9.3-add%20server
func (s *haproxySender) AddServer(ctx context.Context, backend, server string, keywords ...string) error {
	cmd := fmt.Sprintf("add server %s/%s", backend, server)
	response, err := s.sendCommand(ctx, strings.Join(append([]string{cmd}, keywords...), " "))
	switch {
	case err != nil:
		return err
	case slices.Equal(response, []string{"New server registered.", ""}):
		return nil
	default:
		return fmt.Errorf("unexpected response: %q", strings.Join(response, "\n"))
	}
}

// Remove the server from the given backend. The server will implicitly be put
// in maintenance mode prior to its deletion. The operation is cancelled if the
// serveur still has active or idle connection or its connection queue is not
// empty.
//
// https://docs.haproxy.org/2.7/management.html#9.3-del%20server
func (s *haproxySender) DeleteServer(ctx context.Context, backend, server string) (bool, error) {
	response, err := s.sendCommand(ctx, fmt.Sprintf("del server %s/%s", backend, server))
	switch {
	case err != nil:
		return false, fmt.Errorf("failed to delete server: %w", err)
	case slices.Equal(response, []string{"Server deleted."}):
		return true, nil
	case slices.Equal(response, []string{"Server still has connections attached to it, cannot remove it."}):
		return false, nil
	default:
		return false, fmt.Errorf("unexpected response: %q", strings.Join(response, "\n"))
	}
}

// Force a server's administrative state to a new state. This can be useful to
// disable load balancing and/or any traffic to a server. Setting the state to
//
//   - "ready" puts the server in normal mode
//   - "maint" disables any traffic to the server as well as any health checks
//   - "drain" only removes the server from load balancing but still allows it
//     to be checked and to accept new persistent connections
//
// https://docs.haproxy.org/2.7/management.html#9.3-set%20server
func (s *haproxySender) SetServerState(ctx context.Context, backend, server, state string) error {
	response, err := s.sendCommand(ctx, fmt.Sprintf("set server %s/%s state %s", backend, server, state))
	switch {
	case err != nil:
		return err
	case slices.Equal(response, []string{""}):
		return nil
	default:
		return fmt.Errorf("unexpected response: %q", strings.Join(response, "\n"))
	}
}

// This will enable sending of health checks.
//
// https://docs.haproxy.org/2.7/management.html#9.3-enable%20health
func (s *haproxySender) EnableHealth(ctx context.Context, backend, server string) error {
	response, err := s.sendCommand(ctx, fmt.Sprintf("enable health %s/%s", backend, server))
	switch {
	case err != nil:
		return err
	case slices.Equal(response, []string{""}):
		return nil
	default:
		return fmt.Errorf("unexpected response: %q", strings.Join(response, "\n"))
	}
}

func (s *haproxySender) sendCommand(ctx context.Context, cmd string) (lines []string, err error) {
	s.log.Debug("Sending command: ", cmd)
	response, err := s.send(ctx, []byte(cmd+"\n"))
	if err != nil {
		return nil, err
	}
	defer func() { err = multierr.Append(err, response.Close()) }()

	scanner := bufio.NewScanner(bufio.NewReader(response))
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

func sendToUNIXSocket(ctx context.Context, path string, cmd []byte) (conn net.Conn, err error) {
	var dialer net.Dialer
	conn, err = dialer.DialContext(ctx, "unix", path)
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
  stats socket /run/nllb/haproxy.sock mode 666 level admin

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
