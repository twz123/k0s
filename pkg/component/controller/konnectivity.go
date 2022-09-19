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

package controller

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/k0sproject/k0s/internal/pkg/dir"
	"github.com/k0sproject/k0s/internal/pkg/stringmap"
	"github.com/k0sproject/k0s/internal/pkg/sysinfo/machineid"
	"github.com/k0sproject/k0s/internal/pkg/templatewriter"
	"github.com/k0sproject/k0s/internal/pkg/users"
	"github.com/k0sproject/k0s/pkg/apis/k0s.k0sproject.io/v1beta1"
	"github.com/k0sproject/k0s/pkg/assets"
	"github.com/k0sproject/k0s/pkg/component"
	"github.com/k0sproject/k0s/pkg/config"
	"github.com/k0sproject/k0s/pkg/constant"
	kubeutil "github.com/k0sproject/k0s/pkg/kubernetes"
	"github.com/k0sproject/k0s/pkg/supervisor"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/sirupsen/logrus"
)

// Konnectivity implements the component interface of konnectivity server
type Konnectivity struct {
	K0sVars           constant.CfgVars
	APIServer         config.APIServerSpec
	KubeClientFactory kubeutil.ClientFactoryInterface // used for lease lock
	LogLevel          string
	SingleNode        bool

	log *logrus.Entry

	supervisor          *supervisor.Supervisor
	uid                 int
	updates             chan func(*konnectivityState)
	stopFunc            context.CancelFunc
	leaseCounterRunning atomic.Bool
	agentManifestLock   sync.Mutex
}

var _ component.Component = (*Konnectivity)(nil)
var _ component.Reconciler = (*Konnectivity)(nil)

type konnectivityState struct {
	serverCount uint
	cluster     *konnectivityClusterState
	agentConfig konnectivityAgentConfig
}

type konnectivityClusterState struct {
	imageURI   string
	agentPort  uint16
	adminPort  uint16
	pullPolicy corev1.PullPolicy
}

// Init ...
func (k *Konnectivity) Init(_ context.Context) error {
	var err error
	k.uid, err = users.GetUID(constant.KonnectivityServerUser)
	if err != nil {
		logrus.Warning(fmt.Errorf("running konnectivity as root: %w", err))
	}
	err = dir.Init(k.K0sVars.KonnectivitySocketDir, 0755)
	if err != nil {
		return fmt.Errorf("failed to initialize directory %s: %v", k.K0sVars.KonnectivitySocketDir, err)
	}

	err = os.Chown(k.K0sVars.KonnectivitySocketDir, k.uid, -1)
	if err != nil && os.Geteuid() == 0 {
		return fmt.Errorf("failed to chown %s: %v", k.K0sVars.KonnectivitySocketDir, err)
	}

	k.log = logrus.WithFields(logrus.Fields{"component": "konnectivity"})

	return assets.Stage(k.K0sVars.BinDir, "konnectivity-server", constant.BinDirMode)
}

// Run ..
func (k *Konnectivity) Start(ctx context.Context) error {
	// Buffered chan to send updates
	k.updates = make(chan func(*konnectivityState), 1)

	ctx, k.stopFunc = context.WithCancel(ctx)

	go k.runServer(ctx)

	return nil
}

// Reconcile detects changes in configuration and applies them to the component
func (k *Konnectivity) Reconcile(ctx context.Context, clusterCfg *v1beta1.ClusterConfig) error {
	cluster := &konnectivityClusterState{
		imageURI:   clusterCfg.Spec.Images.Konnectivity.URI(),
		agentPort:  uint16(clusterCfg.Spec.Konnectivity.AgentPort),
		adminPort:  uint16(clusterCfg.Spec.Konnectivity.AdminPort),
		pullPolicy: corev1.PullPolicy(clusterCfg.Spec.Images.DefaultPullPolicy),
	}

	if k.SingleNode {
		// It's a buffered channel so once we start the runServer routine it'll pick this up and just sees it never changing
		k.updates <- func(state *konnectivityState) {
			state.serverCount = 1
			state.cluster = cluster
		}
	} else {

		k.updates <- func(state *konnectivityState) { state.cluster = cluster }
		go k.runLeaseCounter(ctx)
	}

	return nil
}

func (k *Konnectivity) defaultArgs(cluster *konnectivityClusterState) stringmap.StringMap {
	machineID, err := machineid.Generate()
	if err != nil {
		logrus.Errorf("failed to fetch machine ID for konnectivity-server")
	}
	return stringmap.StringMap{
		"--uds-name":                filepath.Join(k.K0sVars.KonnectivitySocketDir, "konnectivity-server.sock"),
		"--cluster-cert":            filepath.Join(k.K0sVars.CertRootDir, "server.crt"),
		"--cluster-key":             filepath.Join(k.K0sVars.CertRootDir, "server.key"),
		"--kubeconfig":              k.K0sVars.KonnectivityKubeConfigPath,
		"--mode":                    "grpc",
		"--server-port":             "0",
		"--agent-port":              strconv.FormatUint(uint64(cluster.agentPort), 10),
		"--admin-port":              strconv.FormatUint(uint64(cluster.adminPort), 10),
		"--agent-namespace":         "kube-system",
		"--agent-service-account":   "konnectivity-agent",
		"--authentication-audience": "system:konnectivity-server",
		"--logtostderr":             "true",
		"--stderrthreshold":         "1",
		"--v":                       k.LogLevel,
		"--enable-profiling":        "false",
		"--server-id":               machineID.ID(),
		"--proxy-strategies":        "destHost,default",
	}
}

// runs the supervisor and restarts if the calculated server count changes
func (k *Konnectivity) runServer(ctx context.Context) {
	previousArgs := stringmap.StringMap{}

	var lastState konnectivityState

	for {
		select {
		case <-ctx.Done():
			logrus.Info("stopping konnectivity server reconfig loop")
			return
		case update := <-k.updates:
			state := lastState
			update(&state)
			// restart only if the count actually changes and we've got the global config
			if lastState.serverCount != state.serverCount && state.cluster != nil {
				args := k.defaultArgs(state.cluster)
				args["--server-count"] = strconv.Itoa(int(state.serverCount))
				if args.Equals(previousArgs) {
					logrus.Info("no changes detected for konnectivity-server")
				}
				// Stop supervisor
				if k.supervisor != nil {
					if err := k.supervisor.Stop(); err != nil {
						k.log.WithError(err).Error("Failed to stop supervisor")
						// TODO Should we just return? That means other part will continue to run but the server is never properly restarted
					}
				}

				k.supervisor = &supervisor.Supervisor{
					Name:    "konnectivity",
					BinPath: assets.BinPath("konnectivity-server", k.K0sVars.BinDir),
					DataDir: k.K0sVars.DataDir,
					RunDir:  k.K0sVars.RunDir,
					Args:    args.ToArgs(),
					UID:     k.uid,
				}
				err := k.supervisor.Supervise()
				if err != nil {
					k.log.WithError(err).Error("Failed to start konnectivity supervisor")
					k.supervisor = nil // not to make the next loop to try to stop it first
					continue
				}

				if err := k.writeKonnectivityAgent(&state); err != nil {
					k.log.WithError(err).Error("Failed to update konnectivity-agent template")
				}
			}

			lastState = state
		}
	}
}

// Stop stops
func (k *Konnectivity) Stop() error {
	if k.stopFunc != nil {
		logrus.Debug("closing konnectivity component context")
		k.stopFunc()
	}
	if k.supervisor == nil {
		return nil
	}
	logrus.Debug("about to stop konnectivity supervisor")
	return k.supervisor.Stop()
}

type konnectivityAgentConfig struct {
	ProxyServerHost        string
	ProxyServerPort        uint16
	KASPort                uint16
	Image                  string
	ServerCount            uint
	PullPolicy             string
	TunneledNetworkingMode bool
}

func (k *Konnectivity) writeKonnectivityAgent(state *konnectivityState) error {
	k.agentManifestLock.Lock()
	defer k.agentManifestLock.Unlock()
	konnectivityDir := filepath.Join(k.K0sVars.ManifestsDir, "konnectivity")
	err := dir.Init(konnectivityDir, constant.ManifestsDirMode)
	if err != nil {
		return err
	}

	cfg := konnectivityAgentConfig{
		ProxyServerHost:        k.APIServer.Address(),
		ProxyServerPort:        state.cluster.agentPort,
		KASPort:                uint16(k.APIServer.BindAddress.Port),
		Image:                  state.cluster.imageURI,
		ServerCount:            state.serverCount,
		PullPolicy:             string(state.cluster.pullPolicy),
		TunneledNetworkingMode: k.APIServer.TunneledNetworkingMode,
	}

	if cfg == state.agentConfig {
		k.log.Debug("agent configs match, no need to reconcile")
		return nil
	}

	tw := templatewriter.TemplateWriter{
		Name:     "konnectivity-agent",
		Template: konnectivityAgentTemplate,
		Data:     cfg,
		Path:     filepath.Join(konnectivityDir, "konnectivity-agent.yaml"),
	}
	err = tw.Write()
	if err != nil {
		return fmt.Errorf("failed to write konnectivity agent manifest: %w", err)
	}

	state.agentConfig = cfg
	return nil
}

func (k *Konnectivity) runLeaseCounter(ctx context.Context) {
	if !k.leaseCounterRunning.CompareAndSwap(false, true) {
		return
	}
	defer k.leaseCounterRunning.Store(false)

	k.log.Info("Starting to count controller lease holders every 10 secs")
	_ = wait.PollImmediateUntilWithContext(ctx, 10*time.Second, func(ctx context.Context) (done bool, err error) {
		count, err := k.countLeaseHolders(ctx)
		if err != nil {
			k.log.WithError(err).Error("Failed to count controller leases")
		} else {
			k.updates <- func(state *konnectivityState) {
				state.serverCount = count
			}
		}
		return false, nil
	})
	k.log.Info("Stopped konnectivity lease counter")
}

func (k *Konnectivity) countLeaseHolders(ctx context.Context) (uint, error) {
	client, err := k.KubeClientFactory.GetClient()
	if err != nil {
		return 0, err
	}

	return kubeutil.GetControlPlaneNodeCount(ctx, client)
}

const konnectivityAgentTemplate = `
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: system:konnectivity-server
  labels:
    kubernetes.io/cluster-service: "true"
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: system:auth-delegator
subjects:
  - apiGroup: rbac.authorization.k8s.io
    kind: User
    name: system:konnectivity-server
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: konnectivity-agent
  namespace: kube-system
  labels:
    kubernetes.io/cluster-service: "true"
---
apiVersion: apps/v1
# Alternatively, you can deploy the agents as Deployments. It is not necessary
# to have an agent on each node.
kind: DaemonSet
metadata:
  labels:
    k8s-app: konnectivity-agent
  namespace: kube-system
  name: konnectivity-agent
spec:
  selector:
    matchLabels:
      k8s-app: konnectivity-agent
  template:
    metadata:
      labels:
        k8s-app: konnectivity-agent
      annotations:
        prometheus.io/scrape: 'true'
        prometheus.io/port: '8093'
    spec:
      nodeSelector:
        kubernetes.io/os: linux
      priorityClassName: system-cluster-critical
      tolerations:
        - operator: Exists
      {{ if .TunneledNetworkingMode }}
      hostNetwork: true
      {{ end }}
      containers:
        - image: {{ .Image }}
          imagePullPolicy: {{ .PullPolicy }}
          name: konnectivity-agent
          command: ["/proxy-agent"]
          env:
              # the variable is not in a use
              # we need it to have agent restarted on server count change
              - name: K0S_CONTROLLER_COUNT
                value: "{{ .ServerCount }}"

              - name: NODE_IP
                valueFrom:
                  fieldRef:
                    fieldPath: status.hostIP
          args: [
                  "--logtostderr=true",
                  "--ca-cert=/var/run/secrets/kubernetes.io/serviceaccount/ca.crt",
                  # Since the konnectivity server runs with hostNetwork=true,
                  # this is the IP address of the master machine.
                  "--proxy-server-host={{ .ProxyServerHost }}",
                  "--proxy-server-port={{ .ProxyServerPort }}",
                  "--service-account-token-path=/var/run/secrets/tokens/konnectivity-agent-token",
                  "--agent-identifiers=host=$(NODE_IP)",
                  "--agent-id=$(NODE_IP)",
                  {{ if .TunneledNetworkingMode }}
                  # agent need to listen on the node ip to be on pair with the tunneled network reconciler
                  "--bind-address=$(NODE_IP)",
                  "--apiserver-port-mapping=6443:localhost:{{.KASPort}}"
                  {{ end }} 
                  ]
          volumeMounts:
            - mountPath: /var/run/secrets/tokens
              name: konnectivity-agent-token
          livenessProbe:
            httpGet:
              port: 8093
              path: /healthz
            initialDelaySeconds: 15
            timeoutSeconds: 15
      serviceAccountName: konnectivity-agent
      volumes:
        - name: konnectivity-agent-token
          projected:
            sources:
              - serviceAccountToken:
                  path: konnectivity-agent-token
                  audience: system:konnectivity-server
`
