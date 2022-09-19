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
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/avast/retry-go"
	"github.com/k0sproject/k0s/pkg/telemetry"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"golang.org/x/exp/slices"

	workercmd "github.com/k0sproject/k0s/cmd/worker"
	"github.com/k0sproject/k0s/internal/pkg/dir"
	"github.com/k0sproject/k0s/internal/pkg/file"
	"github.com/k0sproject/k0s/internal/pkg/stringmap"
	"github.com/k0sproject/k0s/internal/pkg/sysinfo"
	"github.com/k0sproject/k0s/pkg/apis/k0s.k0sproject.io/v1beta1"
	"github.com/k0sproject/k0s/pkg/applier"
	"github.com/k0sproject/k0s/pkg/build"
	"github.com/k0sproject/k0s/pkg/certificate"
	"github.com/k0sproject/k0s/pkg/component"
	"github.com/k0sproject/k0s/pkg/component/controller"
	"github.com/k0sproject/k0s/pkg/component/controller/clusterconfig"
	"github.com/k0sproject/k0s/pkg/component/status"
	"github.com/k0sproject/k0s/pkg/component/worker"
	"github.com/k0sproject/k0s/pkg/config"
	"github.com/k0sproject/k0s/pkg/constant"
	"github.com/k0sproject/k0s/pkg/install"
	"github.com/k0sproject/k0s/pkg/kubernetes"
	"github.com/k0sproject/k0s/pkg/performance"
	"github.com/k0sproject/k0s/pkg/token"
)

type controllerCmd struct{ config.CLIOptions }

var ignorePreFlightChecks bool

func NewControllerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "controller [join-token]",
		Short:   "Run controller",
		Aliases: []string{"server"},
		Example: `	Command to associate master nodes:
	CLI argument:
	$ k0s controller [join-token]

	or CLI flag:
	$ k0s controller --token-file [path_to_file]
	Note: Token can be passed either as a CLI argument or as a flag`,
		RunE: func(cmd *cobra.Command, args []string) error {
			c := controllerCmd{config.GetCmdOpts()}

			logrus.SetOutput(os.Stdout)
			if !c.Debug {
				logrus.SetLevel(logrus.InfoLevel)
			}

			if len(args) > 0 {
				c.TokenArg = args[0]
			}
			if len(c.TokenArg) > 0 && len(c.TokenFile) > 0 {
				return fmt.Errorf("you can only pass one token argument either as a CLI argument 'k0s controller [join-token]' or as a flag 'k0s controller --token-file [path]'")
			}
			if len(c.DisableComponents) > 0 {
				for _, cmp := range c.DisableComponents {
					if !slices.Contains(config.AvailableComponents(), cmp) {
						return fmt.Errorf("unknown component %s", cmp)
					}
				}
			}
			if len(c.TokenFile) > 0 {
				bytes, err := os.ReadFile(c.TokenFile)
				if err != nil {
					return err
				}
				c.TokenArg = string(bytes)
			}
			c.Logging = stringmap.Merge(c.CmdLogLevels, c.DefaultLogLevels)
			cmd.SilenceUsage = true

			if err := (&sysinfo.K0sSysinfoSpec{
				ControllerRoleEnabled: true,
				WorkerRoleEnabled:     c.SingleNode || c.EnableWorker,
				DataDir:               c.K0sVars.DataDir,
			}).RunPreFlightChecks(ignorePreFlightChecks); !ignorePreFlightChecks && err != nil {
				return err
			}

			ctx, cancel := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
			defer cancel()
			return c.startController(ctx)
		},
	}

	// append flags
	cmd.Flags().BoolVar(&ignorePreFlightChecks, "ignore-pre-flight-checks", false, "continue even if pre-flight checks fail")
	cmd.Flags().AddFlagSet(config.GetPersistentFlagSet())
	cmd.PersistentFlags().AddFlagSet(config.GetControllerFlags())
	cmd.PersistentFlags().AddFlagSet(config.GetWorkerFlags())
	return cmd
}

func (c *controllerCmd) startController(ctx context.Context) error {
	controlPlane, err := c.LoadControlPlaneSpec()
	if err != nil {
		return err
	}

	c.NodeComponents = component.NewManager()
	c.ClusterComponents = component.NewManager()

	perfTimer := performance.NewTimer("controller-start").Buffer().Start()

	// create directories early with the proper permissions
	if err := dir.Init(c.K0sVars.DataDir, constant.DataDirMode); err != nil {
		return err
	}
	if err := dir.Init(c.K0sVars.CertRootDir, constant.CertRootDirMode); err != nil {
		return err
	}
	// let's make sure run-dir exists
	if err := dir.Init(c.K0sVars.RunDir, constant.RunDirMode); err != nil {
		return fmt.Errorf("failed to initialize dir: %v", err)
	}

	// initialize runtime config
	loadingRules := config.ClientConfigLoadingRules{Nodeconfig: true}
	if err := loadingRules.InitRuntimeConfig(c.K0sVars); err != nil {
		return fmt.Errorf("failed to initialize k0s runtime config: %s", err.Error())
	}

	// from now on, we only refer to the runtime config
	c.CfgFile = loadingRules.RuntimeConfigPath

	certificateManager := certificate.NewManager(c.K0sVars.CertRootDir)

	var joinClient *token.JoinClient

	if c.TokenArg != "" && c.needToJoin() {
		joinClient, err = joinController(ctx, c.TokenArg, c.K0sVars.CertRootDir)
		if err != nil {
			return fmt.Errorf("failed to join controller: %v", err)
		}
	}

	logrus.Info("Binding API server to ", &controlPlane.APIServer.BindAddress)
	logrus.Info("Using SANs ", controlPlane.APIServer.AdditionalSANs)
	clusterDNS, err := controlPlane.Network.DNSAddress()
	if err != nil {
		return err
	}
	logrus.Info("Using Cluster DNS ", clusterDNS)

	switch controlPlane.Storage.Type {
	case config.KineStorageType:
		logrus.Info("Using managed Kine")
		c.NodeComponents.Add(ctx, &controller.Kine{
			Config:  *controlPlane.Storage.Kine,
			K0sVars: c.K0sVars,
		})
	case config.EtcdStorageType:
		logrus.Info("Using managed etcd")
		c.NodeComponents.Add(ctx, &controller.Etcd{
			K0sVars:     c.K0sVars,
			Spec:        *controlPlane.Storage.Etcd,
			CertManager: certificateManager,
			JoinClient:  joinClient,
			LogLevel:    c.Logging["etcd"],
		})

	case config.ExternalEtcdStorageType:
		logrus.Info("Using external etcd")
		c.NodeComponents.Add(ctx, &controller.ExternalEtcd{
			Spec: *controlPlane.Storage.ExternalEtcd,
		})

	default:
		return fmt.Errorf("invalid storage type: %q", controlPlane.Storage.Type)
	}

	// common factory to get the admin kube client that's needed in many components
	adminClientFactory := kubernetes.NewAdminClientFactory(c.K0sVars)
	enableKonnectivity := !c.SingleNode && !slices.Contains(c.DisableComponents, constant.KonnectivityServerComponentName)
	c.NodeComponents.Add(ctx, &controller.APIServer{
		K0sVars:            c.K0sVars,
		ControlPlane:       *controlPlane,
		LogLevel:           c.Logging["kube-apiserver"],
		EnableKonnectivity: enableKonnectivity,
	})

	if !c.SingleNode {
		c.NodeComponents.Add(ctx, &controller.K0sControllersLeaseCounter{
			KubeClientFactory: adminClientFactory,
		})
	}

	var leaderElector interface {
		controller.LeaderElector
		component.Component
	}

	// One leader elector per controller
	if !c.SingleNode {
		leaderElector = controller.NewLeasePoolLeaderElector(adminClientFactory)
	} else {
		leaderElector = &controller.DummyLeaderElector{Leader: true}
	}
	c.NodeComponents.Add(ctx, leaderElector)

	c.NodeComponents.Add(ctx, &applier.Manager{
		K0sVars:           c.K0sVars,
		KubeClientFactory: adminClientFactory,
		LeaderElector:     leaderElector,
	})

	if !c.SingleNode && !slices.Contains(c.DisableComponents, constant.ControlAPIComponentName) {
		c.NodeComponents.Add(ctx, &controller.K0SControlAPI{
			ConfigPath: c.CfgFile,
			K0sVars:    c.K0sVars,
		})
	}

	if !slices.Contains(c.DisableComponents, constant.CsrApproverComponentName) {
		c.NodeComponents.Add(ctx, controller.NewCSRApprover(leaderElector, adminClientFactory))
	}

	if c.EnableK0sCloudProvider {
		c.NodeComponents.Add(
			ctx,
			controller.NewK0sCloudProvider(
				c.K0sVars.AdminKubeConfigPath,
				c.K0sCloudProviderUpdateFrequency,
				c.K0sCloudProviderPort,
			),
		)
	}
	c.NodeComponents.Add(ctx, &status.Status{
		StatusInformation: install.K0sStatus{
			Pid:          os.Getpid(),
			Role:         "controller",
			Args:         os.Args,
			Version:      build.Version,
			Workloads:    c.SingleNode || c.EnableWorker,
			SingleNode:   c.SingleNode,
			ControlPlane: controlPlane,
			RunDir:       c.K0sVars.RunDir,
		},
		Socket:      config.StatusSocket,
		CertManager: worker.NewCertificateManager(ctx, c.K0sVars.KubeletAuthConfigPath),
	})

	perfTimer.Checkpoint("starting-certificates-init")
	certs := &Certificates{
		K0sVars:      c.K0sVars,
		ControlPlane: controlPlane,
		CertManager:  certificateManager,
	}
	if err := certs.Init(ctx); err != nil {
		return err
	}

	perfTimer.Checkpoint("starting-node-component-init")
	// init Node components
	if err := c.NodeComponents.Init(ctx); err != nil {
		return err
	}
	perfTimer.Checkpoint("finished-node-component-init")

	perfTimer.Checkpoint("starting-node-components")

	// Start components
	err = c.NodeComponents.Start(ctx)
	perfTimer.Checkpoint("finished-starting-node-components")
	if err != nil {
		return fmt.Errorf("failed to start controller node components: %w", err)
	}
	defer func() {
		// Stop components
		if err := c.NodeComponents.Stop(); err != nil {
			logrus.WithError(err).Error("Failed to stop node components")
		} else {
			logrus.Info("All node components stopped")
		}
	}()

	var configSource clusterconfig.ConfigSource
	// For backwards compatibility, use file as config source by default
	if c.EnableDynamicConfig {
		configSource, err = clusterconfig.NewAPIConfigSource(adminClientFactory)
	} else {
		clusterConfig, cErr := c.LoadClusterConfig()
		if cErr != nil {
			return cErr
		}
		configSource, err = clusterconfig.NewStaticSource(clusterConfig)
	}
	if err != nil {
		return err
	}
	defer configSource.Stop()

	if !slices.Contains(c.DisableComponents, constant.APIConfigComponentName) {
		apiConfigSaver, err := controller.NewManifestsSaver("api-config", c.K0sVars.DataDir)
		if err != nil {
			return fmt.Errorf("failed to initialize api-config manifests saver: %w", err)
		}

		cfgReconciler, err := controller.NewClusterConfigReconciler(
			leaderElector,
			c.K0sVars,
			c.ClusterComponents,
			apiConfigSaver,
			adminClientFactory,
			configSource,
		)
		if err != nil {
			return fmt.Errorf("failed to initialize cluster-config reconciler: %w", err)
		}
		c.ClusterComponents.Add(ctx, cfgReconciler)
	}

	if !slices.Contains(c.DisableComponents, constant.HelmComponentName) {
		helmSaver, err := controller.NewManifestsSaver("helm", c.K0sVars.DataDir)
		if err != nil {
			return fmt.Errorf("failed to initialize helm manifests saver: %w", err)
		}

		c.ClusterComponents.Add(ctx, controller.NewCRD(helmSaver, []string{"helm"}))
		c.ClusterComponents.Add(ctx, controller.NewExtensionsController(
			helmSaver,
			c.K0sVars,
			adminClientFactory,
			leaderElector,
		))
	}

	if !slices.Contains(c.DisableComponents, constant.AutopilotComponentName) {
		logrus.Debug("starting manifest saver")
		manifestsSaver, err := controller.NewManifestsSaver("autopilot", c.K0sVars.DataDir)
		if err != nil {
			logrus.Warnf("failed to initialize reconcilers manifests saver: %s", err.Error())
			return err
		}
		c.ClusterComponents.Add(ctx, controller.NewCRD(manifestsSaver, []string{"autopilot"}))
	}

	if controlPlane.APIServer.TunneledNetworkingMode {
		c.ClusterComponents.Add(ctx, controller.NewTunneledEndpointReconciler(
			leaderElector,
			adminClientFactory,
		))
	}

	if controlPlane.APIServer.ExternalAddress != "" && !controlPlane.APIServer.TunneledNetworkingMode {
		c.ClusterComponents.Add(ctx, controller.NewEndpointReconciler(
			leaderElector,
			adminClientFactory,
		))
	}

	if !slices.Contains(c.DisableComponents, constant.KubeProxyComponentName) {
		c.ClusterComponents.Add(ctx, controller.NewKubeProxy(&c.K0sVars, *controlPlane.APIServer.URL()))
	}

	if !slices.Contains(c.DisableComponents, constant.CoreDNSComponentname) {
		coreDNS, err := controller.NewCoreDNS(clusterDNS, &c.K0sVars, adminClientFactory)
		if err != nil {
			return fmt.Errorf("failed to create CoreDNS reconciler: %w", err)
		}
		c.ClusterComponents.Add(ctx, coreDNS)
	}

	if !slices.Contains(c.DisableComponents, constant.NetworkProviderComponentName) {
		logrus.Infof("Creating network reconcilers")

		calicoSaver, err := controller.NewManifestsSaver("calico", c.K0sVars.DataDir)
		if err != nil {
			return fmt.Errorf("failed to create calico manifests saver: %w", err)
		}
		calicoInitSaver, err := controller.NewManifestsSaver("calico_init", c.K0sVars.DataDir)
		if err != nil {
			return fmt.Errorf("failed to create calico_init manifests saver: %w", err)
		}
		c.ClusterComponents.Add(ctx, controller.NewCalico(c.K0sVars, calicoInitSaver, calicoSaver))

		kubeRouterSaver, err := controller.NewManifestsSaver("kuberouter", c.K0sVars.DataDir)
		if err != nil {
			return fmt.Errorf("failed to create kuberouter manifests saver: %w", err)
		}
		c.ClusterComponents.Add(ctx, controller.NewKubeRouter(c.K0sVars, kubeRouterSaver))
	}

	if !slices.Contains(c.DisableComponents, constant.MetricsServerComponentName) {
		c.ClusterComponents.Add(ctx, controller.NewMetricServer(c.K0sVars, adminClientFactory))
	}

	if c.EnableMetricsScraper {
		metricsSaver, err := controller.NewManifestsSaver("metrics", c.K0sVars.DataDir)
		if err != nil {
			return fmt.Errorf("failed to create metrics manifests saver: %w", err)
		}
		metrics, err := controller.NewMetrics(c.K0sVars, metricsSaver, adminClientFactory)
		if err != nil {
			return fmt.Errorf("failed to create metrics reconciler: %w", err)
		}
		c.ClusterComponents.Add(ctx, metrics)
	}

	if !slices.Contains(c.DisableComponents, constant.KubeletConfigComponentName) {
		kubeletConfig, err := controller.NewKubeletConfig(&c.K0sVars, &controlPlane.Network, adminClientFactory)
		if err != nil {
			return err
		}
		c.ClusterComponents.Add(ctx, kubeletConfig)
	}

	if !slices.Contains(c.DisableComponents, constant.SystemRbacComponentName) {
		c.ClusterComponents.Add(ctx, controller.NewSystemRBAC(c.K0sVars.ManifestsDir))
	}

	if !slices.Contains(c.DisableComponents, constant.NodeRoleComponentName) {
		c.ClusterComponents.Add(ctx, controller.NewNodeRole(c.K0sVars, adminClientFactory))
	}

	if enableKonnectivity {
		c.ClusterComponents.Add(ctx, &controller.Konnectivity{
			K0sVars:           c.K0sVars,
			APIServer:         controlPlane.APIServer,
			SingleNode:        c.SingleNode,
			LogLevel:          c.Logging[constant.KonnectivityServerComponentName],
			KubeClientFactory: adminClientFactory,
		})
	}

	if !slices.Contains(c.DisableComponents, constant.KubeSchedulerComponentName) {
		c.ClusterComponents.Add(ctx, &controller.Scheduler{
			LogLevel:   c.Logging[constant.KubeSchedulerComponentName],
			K0sVars:    c.K0sVars,
			SingleNode: c.SingleNode,
		})
	}

	if !slices.Contains(c.DisableComponents, constant.KubeControllerManagerComponentName) {
		c.ClusterComponents.Add(ctx, &controller.Manager{
			K0sVars:      c.K0sVars,
			ServiceCIDRs: controlPlane.Network.ServiceCIDRs,
			SingleNode:   c.SingleNode,
			LogLevel:     c.Logging[constant.KubeControllerManagerComponentName],
			ExtraArgs:    c.KubeControllerManagerExtraArgs,
		})
	}

	c.ClusterComponents.Add(ctx, &telemetry.Component{
		Version:           build.Version,
		K0sVars:           c.K0sVars,
		KubeClientFactory: adminClientFactory,
	})

	c.ClusterComponents.Add(ctx, &controller.Autopilot{
		K0sVars:            c.K0sVars,
		AdminClientFactory: adminClientFactory,
		EnableWorker:       c.EnableWorker,
	})

	perfTimer.Checkpoint("starting-cluster-components-init")
	// init Cluster components
	if err := c.ClusterComponents.Init(ctx); err != nil {
		return err
	}
	perfTimer.Checkpoint("finished cluster-component-init")

	err = c.ClusterComponents.Start(ctx)
	if err != nil {
		return fmt.Errorf("failed to start cluster components: %w", err)
	}
	perfTimer.Checkpoint("finished-starting-cluster-components")
	defer func() {
		// Stop Cluster components
		if err := c.ClusterComponents.Stop(); err != nil {
			logrus.WithError(err).Error("Failed to stop cluster components")
		} else {
			logrus.Info("All cluster components stopped")
		}
	}()

	// At this point all the components should be initialized and running, thus we can release the config for reconcilers
	go configSource.Release(ctx)

	if c.EnableWorker {
		perfTimer.Checkpoint("starting-worker")

		if err := c.startControllerWorker(ctx, controlPlane); err != nil {
			logrus.WithError(err).Error("Failed to start controller worker")
		} else {
			perfTimer.Checkpoint("started-worker")
		}
	}

	perfTimer.Output()

	// Wait for k0s process termination
	<-ctx.Done()
	logrus.Debug("Context done in main")
	logrus.Info("Shutting down k0s controller")

	perfTimer.Output()
	return os.Remove(c.CfgFile)
}

func (c *controllerCmd) startControllerWorker(ctx context.Context, controlPlane *config.ControlPlaneSpec) error {
	var bootstrapConfig string
	if !file.Exists(c.K0sVars.KubeletAuthConfigPath) {
		// wait for controller to start up
		err := retry.Do(func() error {
			if !file.Exists(c.K0sVars.AdminKubeConfigPath) {
				return fmt.Errorf("file does not exist: %s", c.K0sVars.AdminKubeConfigPath)
			}
			return nil
		}, retry.Context(ctx))
		if err != nil {
			return err
		}

		err = retry.Do(func() error {
			// five minutes here are coming from maximum theoretical duration of kubelet bootstrap process
			// we use retry.Do with 10 attempts, back-off delay and delay duration 500 ms which gives us
			// 225 seconds here
			tokenAge := time.Second * 225
			cfg, err := token.CreateKubeletBootstrapToken(ctx, controlPlane, c.K0sVars, token.RoleWorker, tokenAge)
			if err != nil {
				return err
			}
			bootstrapConfig = cfg
			return nil
		}, retry.Context(ctx))
		if err != nil {
			return err
		}
	}

	workerCmdOpts := (workercmd.CmdOpts)(c.CLIOptions)
	workerCmdOpts.TokenArg = bootstrapConfig
	workerCmdOpts.WorkerProfile = c.WorkerProfile
	workerCmdOpts.Labels = append(workerCmdOpts.Labels, fmt.Sprintf("%s=control-plane", constant.K0SNodeRoleLabel))
	if !c.SingleNode && !c.NoTaints {
		workerCmdOpts.Taints = append(workerCmdOpts.Taints, fmt.Sprintf("%s/master=:NoSchedule", constant.NodeRoleLabelNamespace))
	}
	return workerCmdOpts.StartWorker(ctx)
}

// If we've got CA in place we assume the node has already joined previously
func (c *controllerCmd) needToJoin() bool {
	if file.Exists(filepath.Join(c.K0sVars.CertRootDir, "ca.key")) &&
		file.Exists(filepath.Join(c.K0sVars.CertRootDir, "ca.crt")) {
		return false
	}
	return true
}

func writeCerts(caData v1beta1.CaResponse, certRootDir string) error {
	type fileData struct {
		path string
		data []byte
		mode fs.FileMode
	}
	for _, f := range []fileData{
		{path: filepath.Join(certRootDir, "ca.key"), data: caData.Key, mode: constant.CertSecureMode},
		{path: filepath.Join(certRootDir, "ca.crt"), data: caData.Cert, mode: constant.CertMode},
		{path: filepath.Join(certRootDir, "sa.key"), data: caData.SAKey, mode: constant.CertSecureMode},
		{path: filepath.Join(certRootDir, "sa.pub"), data: caData.SAPub, mode: constant.CertMode},
	} {
		err := os.WriteFile(f.path, f.data, f.mode)
		if err != nil {
			return fmt.Errorf("failed to write %s: %w", f.path, err)
		}
	}
	return nil
}

func joinController(ctx context.Context, tokenArg string, certRootDir string) (*token.JoinClient, error) {
	joinClient, err := token.JoinClientFromToken(tokenArg)
	if err != nil {
		return nil, fmt.Errorf("failed to create join client: %w", err)
	}

	if joinClient.JoinTokenType() != "controller-bootstrap" {
		return nil, fmt.Errorf("wrong token type %s, expected type: controller-bootstrap", joinClient.JoinTokenType())
	}

	var caData v1beta1.CaResponse
	err = retry.Do(func() error {
		caData, err = joinClient.GetCA()
		if err != nil {
			return fmt.Errorf("failed to sync CA: %w", err)
		}
		return nil
	}, retry.Context(ctx))
	if err != nil {
		return nil, err
	}
	return joinClient, writeCerts(caData, certRootDir)
}
