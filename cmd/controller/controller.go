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

	"github.com/k0sproject/k0s/pkg/install"

	"github.com/avast/retry-go"
	"github.com/k0sproject/k0s/pkg/telemetry"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	workercmd "github.com/k0sproject/k0s/cmd/worker"
	"github.com/k0sproject/k0s/internal/pkg/dir"
	"github.com/k0sproject/k0s/internal/pkg/file"
	"github.com/k0sproject/k0s/internal/pkg/stringmap"
	"github.com/k0sproject/k0s/internal/pkg/stringslice"
	"github.com/k0sproject/k0s/internal/pkg/sysinfo"
	"github.com/k0sproject/k0s/pkg/apis/k0s.k0sproject.io/v1beta1"
	"github.com/k0sproject/k0s/pkg/applier"
	"github.com/k0sproject/k0s/pkg/build"
	"github.com/k0sproject/k0s/pkg/certificate"
	"github.com/k0sproject/k0s/pkg/component"
	"github.com/k0sproject/k0s/pkg/component/controller"
	"github.com/k0sproject/k0s/pkg/component/controller/clusterconfig"
	"github.com/k0sproject/k0s/pkg/component/status"
	"github.com/k0sproject/k0s/pkg/config"
	"github.com/k0sproject/k0s/pkg/constant"
	"github.com/k0sproject/k0s/pkg/kubernetes"
	"github.com/k0sproject/k0s/pkg/performance"
	"github.com/k0sproject/k0s/pkg/token"
)

type CmdOpts config.CLIOptions

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
			logrus.SetLevel(logrus.InfoLevel)
			logrus.SetOutput(os.Stdout)

			c := CmdOpts(config.GetCmdOpts())
			if len(args) > 0 {
				c.TokenArg = args[0]
			}
			if len(c.TokenArg) > 0 && len(c.TokenFile) > 0 {
				return fmt.Errorf("you can only pass one token argument either as a CLI argument 'k0s controller [join-token]' or as a flag 'k0s controller --token-file [path]'")
			}
			if len(c.DisableComponents) > 0 {
				for _, cmp := range c.DisableComponents {
					if !stringslice.Contains(config.AvailableComponents(), cmp) {
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

func (c *CmdOpts) startController(ctx context.Context) error {
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

	certificateManager := certificate.Manager{K0sVars: c.K0sVars}

	var joinClient *token.JoinClient
	var err error

	if c.TokenArg != "" && c.needToJoin() {
		joinClient, err = joinController(ctx, c.TokenArg, c.K0sVars.CertRootDir)
		if err != nil {
			return fmt.Errorf("failed to join controller: %v", err)
		}
	}

	logrus.Infof("using api address: %s", c.NodeConfig.Spec.API.Address)
	logrus.Infof("using listen port: %d", c.NodeConfig.Spec.API.Port)
	logrus.Infof("using sans: %s", c.NodeConfig.Spec.API.SANs)
	dnsAddress, err := c.NodeConfig.Spec.Network.DNSAddress()
	if err != nil {
		return err
	}
	logrus.Infof("DNS address: %s", dnsAddress)

	nodeComponents := component.NewManager()

	var storageBackend component.Component

	switch c.NodeConfig.Spec.Storage.Type {
	case v1beta1.KineStorageType:
		storageBackend = &controller.Kine{
			Config:  c.NodeConfig.Spec.Storage.Kine,
			K0sVars: c.K0sVars,
		}
	case v1beta1.EtcdStorageType:
		storageBackend = &controller.Etcd{
			CertManager: certificateManager,
			Config:      c.NodeConfig.Spec.Storage.Etcd,
			JoinClient:  joinClient,
			K0sVars:     c.K0sVars,
			LogLevel:    c.Logging["etcd"],
		}
	default:
		return fmt.Errorf("invalid storage type: %s", c.NodeConfig.Spec.Storage.Type)
	}
	logrus.Infof("using storage backend %s", c.NodeConfig.Spec.Storage.Type)
	nodeComponents.Add(ctx, storageBackend)

	// common factory to get the admin kube client that's needed in many components
	adminClientFactory := kubernetes.NewAdminClientFactory(c.K0sVars)
	enableKonnectivity := !c.SingleNode && !stringslice.Contains(c.DisableComponents, constant.KonnectivityServerComponentName)
	nodeComponents.Add(ctx, &controller.APIServer{
		ClusterConfig:      c.NodeConfig,
		K0sVars:            c.K0sVars,
		LogLevel:           c.Logging["kube-apiserver"],
		Storage:            storageBackend,
		EnableKonnectivity: enableKonnectivity,
	})

	if !c.SingleNode {
		nodeComponents.Add(ctx, &controller.K0sControllersLeaseCounter{
			ClusterConfig:     c.NodeConfig,
			KubeClientFactory: adminClientFactory,
		})
	}

	var leaderElector interface {
		controller.LeaderElector
		component.Component
	}

	// One leader elector per controller
	if !c.SingleNode {
		leaderElector = controller.NewLeaderElector(adminClientFactory)
	} else {
		leaderElector = &controller.DummyLeaderElector{Leader: true}
	}
	nodeComponents.Add(ctx, leaderElector)

	nodeComponents.Add(ctx, &applier.Manager{
		K0sVars:           c.K0sVars,
		KubeClientFactory: adminClientFactory,
		LeaderElector:     leaderElector,
	})

	if !c.SingleNode && !stringslice.Contains(c.DisableComponents, constant.ControlAPIComponentName) {
		nodeComponents.Add(ctx, &controller.K0SControlAPI{
			ConfigPath: c.CfgFile,
			K0sVars:    c.K0sVars,
		})
	}

	clusterComponents := component.NewManager()

	if c.NodeConfig.Spec.API.TunneledNetworkingMode {
		clusterComponents.Add(ctx, controller.NewTunneledEndpointReconciler(leaderElector,
			adminClientFactory))
	}

	if c.NodeConfig.Spec.API.ExternalAddress != "" && !c.NodeConfig.Spec.API.TunneledNetworkingMode {
		clusterComponents.Add(ctx, controller.NewEndpointReconciler(
			leaderElector,
			adminClientFactory,
		))
	}
	if !stringslice.Contains(c.DisableComponents, constant.CsrApproverComponentName) {
		nodeComponents.Add(ctx, controller.NewCSRApprover(c.NodeConfig,
			leaderElector,
			adminClientFactory))
	}

	if c.EnableK0sCloudProvider {
		nodeComponents.Add(
			ctx,
			controller.NewK0sCloudProvider(
				c.K0sVars.AdminKubeConfigPath,
				c.K0sCloudProviderUpdateFrequency,
				c.K0sCloudProviderPort,
			),
		)
	}

	nodeComponents.Add(ctx, &status.Status{
		StatusInformation: install.K0sStatus{
			Pid:           os.Getpid(),
			Role:          "controller",
			Args:          os.Args,
			Version:       build.Version,
			Workloads:     c.SingleNode || c.EnableWorker,
			SingleNode:    c.SingleNode,
			K0sVars:       c.K0sVars,
			ClusterConfig: c.NodeConfig,
		},
		Socket: config.StatusSocket,
	})

	perfTimer.Checkpoint("starting-certificates-init")
	certs := &Certificates{
		ClusterSpec: c.NodeConfig.Spec,
		CertManager: certificateManager,
		K0sVars:     c.K0sVars,
	}
	if err := certs.Init(ctx); err != nil {
		return err
	}

	perfTimer.Checkpoint("starting-node-component-init")
	// init Node components
	if err := nodeComponents.Init(ctx); err != nil {
		return err
	}
	perfTimer.Checkpoint("finished-node-component-init")

	perfTimer.Checkpoint("starting-node-components")

	// Start components
	err = nodeComponents.Start(ctx)
	perfTimer.Checkpoint("finished-starting-node-components")
	if err != nil {
		return fmt.Errorf("failed to start controller node components: %w", err)
	}
	defer func() {
		// Stop components
		if stopErr := nodeComponents.Stop(); stopErr != nil {
			logrus.Errorf("error while stopping node component %s", stopErr)
		} else {
			logrus.Info("all node components stopped")
		}
	}()

	// in-cluster component reconcilers
	if clusterReconcilers, err := c.createClusterReconcilers(ctx, adminClientFactory); err != nil {
		return err
	} else {
		// Init and add all components to clusterComponents manager
		for name, component := range clusterReconcilers {
			logrus.Infof("Adding %s as a cluster component to be reconciled based on the configuration object", name)
			clusterComponents.Add(ctx, component)
		}
	}

	if enableKonnectivity {
		clusterComponents.Add(ctx, &controller.Konnectivity{
			SingleNode:        c.SingleNode,
			LogLevel:          c.Logging[constant.KonnectivityServerComponentName],
			K0sVars:           c.K0sVars,
			KubeClientFactory: adminClientFactory,
			NodeConfig:        c.NodeConfig,
		})
	}

	if !stringslice.Contains(c.DisableComponents, constant.KubeSchedulerComponentName) {
		clusterComponents.Add(ctx, &controller.Scheduler{
			LogLevel:   c.Logging[constant.KubeSchedulerComponentName],
			K0sVars:    c.K0sVars,
			SingleNode: c.SingleNode,
		})
	}

	if !stringslice.Contains(c.DisableComponents, constant.KubeControllerManagerComponentName) {
		clusterComponents.Add(ctx, &controller.Manager{
			LogLevel:   c.Logging[constant.KubeControllerManagerComponentName],
			K0sVars:    c.K0sVars,
			SingleNode: c.SingleNode,
		})
	}

	clusterComponents.Add(ctx, &telemetry.Component{
		Version:           build.Version,
		K0sVars:           c.K0sVars,
		KubeClientFactory: adminClientFactory,
	})

	perfTimer.Checkpoint("starting-cluster-components-init")
	// init Cluster components
	if err := clusterComponents.Init(ctx); err != nil {
		return err
	}
	perfTimer.Checkpoint("finished cluster-component-init")

	var cfgSource clusterconfig.ConfigSource
	// For backwards compatibility, use file as config source by default
	if c.EnableDynamicConfig {
		cfgSource, err = clusterconfig.NewAPIConfigSource(adminClientFactory)
		if err != nil {
			return err
		}
	} else {
		cfgSource, err = clusterconfig.NewStaticSource(c.ClusterConfig)
		if err != nil {
			return err
		}
	}
	defer func() {
		if cfgSource != nil {
			if err := cfgSource.Stop(); err != nil {
				logrus.Warnf("error while stopping ConfigSource: %s", err.Error())
			}
		}
	}()

	// start Bootstrapping Reconcilers
	err = c.startBootstrapReconcilers(ctx, clusterComponents, adminClientFactory, leaderElector, cfgSource)
	if err != nil {
		return fmt.Errorf("failed to start bootstrapping reconcilers: %w", err)
	}

	err = clusterComponents.Start(ctx)
	if err != nil {
		return fmt.Errorf("failed to start cluster components: %w", err)
	}
	perfTimer.Checkpoint("finished-starting-cluster-components")
	defer func() {
		// Stop Cluster components
		if stopErr := clusterComponents.Stop(); stopErr != nil {
			logrus.Errorf("error while stopping node component %s", stopErr)
		}
		logrus.Info("all cluster components stopped")
	}()

	// At this point all the components should be initialized and running, thus we can release the config for reconcilers
	errCh := make(chan error, 1)
	go func() {
		if err := cfgSource.Release(ctx); err != nil {
			// If the config release does not work, nothing is going to work
			errCh <- fmt.Errorf("failed to release configuration for reconcilers: %w", err)
		}
	}()

	if c.EnableWorker {
		perfTimer.Checkpoint("starting-worker")

		err = c.startControllerWorker(ctx, c.WorkerProfile)
		if err != nil {
			errCh <- fmt.Errorf("failed to start controller worker: %w", err)
		}
	}
	perfTimer.Checkpoint("started-worker")

	perfTimer.Output()

	// Wait for k0s process termination
	select {
	case err = <-errCh:
		logrus.WithError(err).Error("Failed to start controller")
	case <-ctx.Done():
		logrus.Debug("Context done in main")
	}
	logrus.Info("Shutting down k0s controller")

	perfTimer.Output()
	return os.Remove(c.CfgFile)
}

func (c *CmdOpts) startBootstrapReconcilers(ctx context.Context, clusterComponents *component.Manager, cf kubernetes.ClientFactoryInterface, leaderElector controller.LeaderElector, configSource clusterconfig.ConfigSource) error {
	reconcilers := make(map[string]component.Component)

	if !stringslice.Contains(c.DisableComponents, constant.APIConfigComponentName) {
		logrus.Debug("Creating cluster config reconciler")
		clusterConfig, err := c.newClusterConfigReconciler(leaderElector, clusterComponents, cf, configSource)
		if err != nil {
			return fmt.Errorf("failed to create cluster config reconciler: %w", err)
		}
		reconcilers["clusterConfig"] = clusterConfig
	}

	if !stringslice.Contains(c.DisableComponents, constant.HelmComponentName) {
		logrus.Debug("starting manifest saver")
		manifestsSaver, err := controller.NewManifestsSaver("helm", c.K0sVars.DataDir)
		if err != nil {
			return fmt.Errorf("failed to create helm manifests saver: %w", err)
		}
		reconcilers["helmCrd"] = controller.NewCRD(manifestsSaver)
		clusterComponents.Add(ctx, controller.NewExtensionsController(manifestsSaver, c.K0sVars, cf, leaderElector))
	}

	// Start all reconcilers
	for name, reconciler := range reconcilers {
		logrus.Info("Initializing bootstrap reconciler: ", name)
		err := reconciler.Init(ctx)
		if err != nil {
			return fmt.Errorf("failed to initialize bootstrap reconciler %s: %w", name, err)
		}
	}
	for name, reconciler := range reconcilers {
		logrus.Info("Running bootstrap reconciler: ", name)
		err := reconciler.Run(ctx)
		if err != nil {
			return fmt.Errorf("failed to run bootstrap reconciler %s: %w", name, err)
		}
	}

	return nil
}

func (c *CmdOpts) createClusterReconcilers(ctx context.Context, cf kubernetes.ClientFactoryInterface) (map[string]component.Component, error) {
	reconcilers := make(map[string]component.Component)

	if !stringslice.Contains(c.DisableComponents, constant.DefaultPspComponentName) {
		reconcilers["default-psp"] = controller.NewDefaultPSP(c.K0sVars)
	}

	if !stringslice.Contains(c.DisableComponents, constant.KubeProxyComponentName) {
		reconcilers["kube-proxy"] = controller.NewKubeProxy(c.CfgFile, c.K0sVars, c.NodeConfig)
	}

	if !stringslice.Contains(c.DisableComponents, constant.CoreDNSComponentname) {
		coreDNS, err := controller.NewCoreDNS(c.K0sVars, cf)
		if err != nil {
			return nil, fmt.Errorf("failed to create CoreDNS reconciler: %w", err)
		}
		reconcilers["coredns"] = coreDNS
	}

	if !stringslice.Contains(c.DisableComponents, constant.NetworkProviderComponentName) {
		logrus.Infof("Creating network reconcilers")
		if calico, err := c.newCalico(); err == nil {
			reconcilers["calico"] = calico
		} else {
			logrus.WithError(err).Warn("Failed to create Calico reconciler")
		}

		if kubeRouter, err := c.newKubeRouter(); err == nil {
			reconcilers["kube-router"] = kubeRouter
		} else {
			logrus.WithError(err).Warn("Failed to create kube-router reconciler")
		}
	}

	if !stringslice.Contains(c.DisableComponents, constant.MetricsServerComponentName) {
		reconcilers["metricServer"] = controller.NewMetricServer(c.K0sVars, cf)
	}

	if c.EnableMetricsScraper {
		metrics, err := c.newMetrics(cf)
		if err != nil {
			return nil, fmt.Errorf("failed to create metrics reconciler: %w", err)
		}
		reconcilers["metrics"] = metrics
	}

	if !stringslice.Contains(c.DisableComponents, constant.KubeletConfigComponentName) {
		reconcilers["kubeletConfig"] = controller.NewKubeletConfig(c.K0sVars, cf)
	}

	if !stringslice.Contains(c.DisableComponents, constant.SystemRbacComponentName) {
		reconcilers["systemRBAC"] = controller.NewSystemRBAC(c.K0sVars.ManifestsDir)
	}

	if !stringslice.Contains(c.DisableComponents, constant.NodeRoleComponentName) {
		reconcilers["roleLabeler"] = controller.NewNodeRole(c.K0sVars, cf)
	}

	return reconcilers, nil
}

func (c *CmdOpts) newCalico() (component.Component, error) {
	saver, err := controller.NewManifestsSaver("calico", c.K0sVars.DataDir)
	if err != nil {
		return nil, fmt.Errorf("failed to create manifests saver: %w", err)
	}
	initSaver, err := controller.NewManifestsSaver("calico_init", c.K0sVars.DataDir)
	if err != nil {
		return nil, fmt.Errorf("failed to create init manifests saver: %w", err)
	}

	return controller.NewCalico(c.K0sVars, initSaver, saver), nil
}

func (c *CmdOpts) newKubeRouter() (component.Component, error) {
	saver, err := controller.NewManifestsSaver("kuberouter", c.K0sVars.DataDir)
	if err != nil {
		return nil, fmt.Errorf("failed to create manifests saver: %w", err)
	}

	return controller.NewKubeRouter(c.K0sVars, saver), nil
}

func (c *CmdOpts) newMetrics(cf kubernetes.ClientFactoryInterface) (component.Component, error) {
	saver, err := controller.NewManifestsSaver("metrics", c.K0sVars.DataDir)
	if err != nil {
		return nil, fmt.Errorf("failed to create manifests saver: %w", err)
	}

	return controller.NewMetrics(c.K0sVars, saver, cf)
}

func (c *CmdOpts) newClusterConfigReconciler(leaderElector controller.LeaderElector, clusterComponents *component.Manager, cf kubernetes.ClientFactoryInterface, configSource clusterconfig.ConfigSource) (component.Component, error) {
	saver, err := controller.NewManifestsSaver("api-config", c.K0sVars.DataDir)
	if err != nil {
		return nil, fmt.Errorf("failed to create manifests saver: %w", err)
	}

	return controller.NewClusterConfigReconciler(leaderElector, c.K0sVars, clusterComponents, saver, cf, configSource)
}

func (c *CmdOpts) startControllerWorker(ctx context.Context, profile string) error {
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
			cfg, err := token.CreateKubeletBootstrapConfig(ctx, c.NodeConfig.Spec.API, c.K0sVars, token.RoleWorker, tokenAge)
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
	// cast and make a copy of the controller cmdOpts
	// so we can use the same opts to start the worker
	// Needs to be a copy so we don't mess up the original
	// token and possibly other args
	workerCmdOpts := *(*workercmd.CmdOpts)(c)
	workerCmdOpts.TokenArg = bootstrapConfig
	workerCmdOpts.WorkerProfile = profile
	workerCmdOpts.Labels = append(workerCmdOpts.Labels, fmt.Sprintf("%s=control-plane", constant.K0SNodeRoleLabel))
	if !c.SingleNode && !c.NoTaints {
		workerCmdOpts.Taints = append(workerCmdOpts.Taints, fmt.Sprintf("%s/master=:NoSchedule", constant.NodeRoleLabelNamespace))
	}
	return workerCmdOpts.StartWorker(ctx)
}

// If we've got CA in place we assume the node has already joined previously
func (c *CmdOpts) needToJoin() bool {
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
