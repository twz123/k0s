//go:build unix

// SPDX-FileCopyrightText: 2021 k0s authors
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"fmt"
	"time"

	apv1beta2 "github.com/k0sproject/k0s/pkg/apis/autopilot/v1beta2"
	apcli "github.com/k0sproject/k0s/pkg/autopilot/client"
	apconst "github.com/k0sproject/k0s/pkg/autopilot/constant"
	apdel "github.com/k0sproject/k0s/pkg/autopilot/controller/delegate"
	"github.com/k0sproject/k0s/pkg/autopilot/controller/plans"
	aproot "github.com/k0sproject/k0s/pkg/autopilot/controller/root"
	"github.com/k0sproject/k0s/pkg/autopilot/controller/signal"
	"github.com/k0sproject/k0s/pkg/autopilot/controller/updates"
	"github.com/k0sproject/k0s/pkg/kubernetes"

	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	"k8s.io/utils/ptr"
	cr "sigs.k8s.io/controller-runtime"
	crcli "sigs.k8s.io/controller-runtime/pkg/client"
	crconfig "sigs.k8s.io/controller-runtime/pkg/config"
	crman "sigs.k8s.io/controller-runtime/pkg/manager"
	crmetricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	crwebhook "sigs.k8s.io/controller-runtime/pkg/webhook"
)

type subControllerStartFunc func(ctx context.Context) (context.CancelFunc, *errgroup.Group)
type subControllerStartRoutineFunc func(ctx context.Context, logger *logrus.Entry) error
type subControllerStopFunc func(cancel context.CancelFunc, g *errgroup.Group)
type setupFunc func(ctx context.Context, cf apcli.FactoryInterface) error

type rootController struct {
	cfg                    aproot.RootConfig
	log                    *logrus.Entry
	kubeClientFactory      kubernetes.ClientFactoryInterface
	autopilotClientFactory apcli.FactoryInterface

	startSubHandler        subControllerStartFunc
	startSubHandlerRoutine subControllerStartRoutineFunc
	stopSubHandler         subControllerStopFunc
	setupHandler           setupFunc

	initialized bool
}

var _ aproot.Root = (*rootController)(nil)

// NewRootController builds a root for autopilot "controller" operations.
func NewRootController(cfg aproot.RootConfig, logger *logrus.Entry, enableWorker bool, cf kubernetes.ClientFactoryInterface, acf apcli.FactoryInterface) (aproot.Root, error) {
	c := &rootController{
		cfg:                    cfg,
		log:                    logger,
		autopilotClientFactory: acf,
		kubeClientFactory:      cf,
	}

	// Default implementations that can be overridden for testing.
	c.startSubHandler = c.startSubControllers
	c.startSubHandlerRoutine = c.startSubControllerRoutine
	c.stopSubHandler = c.stopSubControllers
	c.setupHandler = func(ctx context.Context, cf apcli.FactoryInterface) error {
		setupController := NewSetupController(c.log, cf, cfg.K0sDataDir, cfg.KubeletExtraArgs, enableWorker)
		return setupController.Run(ctx)
	}

	return c, nil
}

func (c *rootController) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	_ = cancel

	// Create / initialize kubernetes objects as needed
	if err := c.setupHandler(ctx, c.autopilotClientFactory); err != nil {
		return fmt.Errorf("setup controller failed to complete: %w", err)
	}

	// Start controllers
	subControllerCancel, subControllerErrGroup := c.startSubHandler(ctx)
	defer c.stopSubHandler(subControllerCancel, subControllerErrGroup)

	<-ctx.Done()
	c.log.Info("Shutting down: ", context.Cause(ctx))

	return nil
}

// startSubControllerRoutine is what is executed by default by `startSubControllers`.
// This creates the controller-runtime manager, registers all required components,
// and starts it in a goroutine.
func (c *rootController) startSubControllerRoutine(ctx context.Context, logger *logrus.Entry) error {
	kubeClient, err := c.autopilotClientFactory.GetClient()
	if err != nil {
		return fmt.Errorf("failed to get Kubernetes client: %w", err)
	}

	managerOpts := crman.Options{
		Scheme: scheme,

		LeaderElection: true,
		LeaderElectionResourceLockInterface: &resourcelock.LeaseLock{
			LeaseMeta: metav1.ObjectMeta{
				Namespace: apconst.AutopilotNamespace,
				Name:      apconst.AutopilotNamespace + "-controller",
			},
			Client: kubeClient.CoordinationV1(),
			LockConfig: resourcelock.ResourceLockConfig{
				Identity: c.cfg.InvocationID,
			},
		},

		Controller: crconfig.Controller{
			// If this controller is already initialized, this means that all
			// controller-runtime controllers have already been successfully
			// registered to another manager. However, controller-runtime
			// maintains a global checklist of controller names and doesn't
			// currently provide a way to unregister names from discarded
			// managers. So it's necessary to suppress the global name check
			// whenever things are restarted for reconfiguration.
			SkipNameValidation: ptr.To(c.initialized),

			// Make leader election opt-in per controller.
			NeedLeaderElection: ptr.To(false),
		},

		WebhookServer: crwebhook.NewServer(crwebhook.Options{
			Port: c.cfg.ManagerPort,
		}),
		Metrics: crmetricsserver.Options{
			BindAddress: c.cfg.MetricsBindAddr,
		},
		HealthProbeBindAddress: c.cfg.HealthProbeBindAddr,
	}

	restConfig, err := c.autopilotClientFactory.GetRESTConfig()
	if err != nil {
		return err
	}

	mgr, err := cr.NewManager(restConfig, managerOpts)
	if err != nil {
		logger.WithError(err).Error("unable to start controller manager")
		return err
	}

	if err := RegisterIndexers(ctx, mgr, "controller"); err != nil {
		logger.WithError(err).Error("unable to register indexers")
		return err
	}

	prober, err := NewReadyProber(logger, c.autopilotClientFactory, mgr.GetConfig(), c.cfg.KubeAPIPort, 1*time.Minute)
	if err != nil {
		logger.WithError(err).Error("unable to create controller prober")
		return err
	}

	delegateMap := map[string]apdel.ControllerDelegate{
		apdel.ControllerDelegateWorker: apdel.NodeControllerDelegate(),
		apdel.ControllerDelegateController: apdel.ControlNodeControllerDelegate(apdel.WithReadyForUpdateFunc(
			func(status apv1beta2.PlanCommandK0sUpdateStatus, obj crcli.Object) apdel.K0sUpdateReadyStatus {
				prober.AddTargets(status.Controllers)

				if err := prober.Probe(); err != nil {
					logger.WithError(err).Error("Plan can not be applied to controllers (failed unanimous)")
					return apdel.Inconsistent
				}

				return apdel.CanUpdate
			},
		)),
	}

	cl, err := c.autopilotClientFactory.GetClient()
	if err != nil {
		return err
	}
	ns, err := cl.CoreV1().Namespaces().Get(ctx, metav1.NamespaceSystem, metav1.GetOptions{})
	if err != nil {
		return err
	}
	clusterID := string(ns.UID)

	if err := signal.RegisterControllers(ctx, logger, mgr, delegateMap[apdel.ControllerDelegateController], c.cfg.K0sDataDir, clusterID); err != nil {
		logger.WithError(err).Error("unable to register signal controllers")
		return err
	}

	if err := plans.RegisterControllers(ctx, logger, mgr, c.kubeClientFactory, delegateMap, c.cfg.ExcludeFromPlans); err != nil {
		logger.WithError(err).Error("unable to register plans controllers")
		return err
	}

	if err := updates.RegisterControllers(ctx, logger, mgr, c.autopilotClientFactory, clusterID); err != nil {
		logger.WithError(err).Error("unable to register updates controllers")
		return err
	}

	// All the controller-runtime controllers have been registered.
	c.initialized = true

	// The controller-runtime start blocks until the context is canceled.
	if err := mgr.Start(ctx); err != nil {
		logger.WithError(err).Error("unable to run controller-runtime manager")
		return err
	}

	return nil
}

// startSubControllers starts all of the controllers specific to the leader mode.
// It is expected that this function runs to completion.
func (c *rootController) startSubControllers(ctx context.Context) (context.CancelFunc, *errgroup.Group) {
	c.log.Info("Starting subcontrollers")

	ctx, cancel := context.WithCancel(ctx)

	g, ctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		c.log.Info("Starting controller-runtime subhandlers")
		if err := c.startSubHandlerRoutine(ctx, c.log); err != nil {
			return fmt.Errorf("failed to start subhandlers: %w", err)
		}
		return nil
	})

	return cancel, g
}

// startSubControllers stop all of the controllers specific to the leader mode.
func (c *rootController) stopSubControllers(cancel context.CancelFunc, g *errgroup.Group) {
	c.log.Info("Stopping subcontrollers")

	if cancel != nil {
		cancel()
		if err := g.Wait(); err != nil {
			c.log.Error(err)
		}
	}
}
