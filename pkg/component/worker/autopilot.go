//go:build unix

// SPDX-FileCopyrightText: 2022 k0s authors
// SPDX-License-Identifier: Apache-2.0

package worker

import (
	"context"
	"errors"
	"fmt"
	"time"

	apcli "github.com/k0sproject/k0s/pkg/autopilot/client"
	autopilotconstant "github.com/k0sproject/k0s/pkg/autopilot/constant"
	apcont "github.com/k0sproject/k0s/pkg/autopilot/controller"
	aproot "github.com/k0sproject/k0s/pkg/autopilot/controller/root"
	"github.com/k0sproject/k0s/pkg/component/manager"
	"github.com/k0sproject/k0s/pkg/kubernetes"
	"github.com/k0sproject/k0s/pkg/kubernetes/watch"

	coordinationv1 "k8s.io/api/coordination/v1"
	coordinationv1client "k8s.io/client-go/kubernetes/typed/coordination/v1"

	"github.com/sirupsen/logrus"
)

var _ manager.Component = (*Autopilot)(nil)

type Autopilot struct {
	DataDir       string
	ClientFactory kubernetes.ClientFactoryInterface

	stop func()
}

func (a *Autopilot) Init(ctx context.Context) error {
	return nil
}

func (a *Autopilot) Start(ctx context.Context) error {
	log := logrus.WithField("component", "autopilot")

	clients, err := a.ClientFactory.GetClient()
	if err != nil {
		return err
	}

	stopErr := errors.New("Autopilot is stopping")
	ctx, cancel := context.WithCancelCause(context.Background())
	done := make(chan struct{})

	go func() {
		defer close(done)

		for {
			err := a.run(ctx, log, clients.CoordinationV1().Leases(autopilotconstant.AutopilotNamespace))
			switch {
			case errors.Is(err, stopErr):
				return
			case err != nil:
				log.WithError(err).Error("Failed to run Autopilot")
			default:
				log.Error("Autopilot returned unexpectedly")
			}

			select {
			case <-ctx.Done():
				return
			case <-time.After(30 * time.Second):
				log.Info("Starting over")
			}
		}
	}()

	a.stop = func() { cancel(stopErr); <-done }

	return nil
}

// Stop stops Autopilot
func (a *Autopilot) Stop() error {
	if stop := a.stop; stop != nil {
		stop()
	}
	return nil
}

func (a *Autopilot) run(ctx context.Context, log *logrus.Entry, leases coordinationv1client.LeaseInterface) error {
	log.Info("Waiting for Autopilot controller")
	if err := a.waitForLease(ctx, log, leases); err != nil {
		return fmt.Errorf("while waiting for Autopilot controller: %w", err)
	}

	autopilotRoot, err := apcont.NewRootWorker(aproot.RootConfig{
		K0sDataDir:          a.DataDir,
		Mode:                "worker",
		ManagerPort:         8899,
		MetricsBindAddr:     "0",
		HealthProbeBindAddr: "0",
	}, log, &apcli.ClientFactory{ClientFactoryInterface: a.ClientFactory})
	if err != nil {
		return err
	}

	return autopilotRoot.Run(ctx)
}

func (a *Autopilot) waitForLease(ctx context.Context, log logrus.FieldLogger, leases coordinationv1client.LeaseInterface) error {
	ctx, cancel := context.WithTimeoutCause(ctx, 5*time.Minute, fmt.Errorf("%w: no active Autopilot controller found", errors.ErrUnsupported))
	defer cancel()

	return watch.Leases(leases).
		WithObjectName(autopilotconstant.AutopilotNamespace+"-controller").
		WithErrorCallback(func(err error) (time.Duration, error) {
			if retryAfter, e := watch.IsRetryable(err); e == nil {
				log.WithError(err).Infof("Transient error while waiting for Autopilot controller, starting over after %s ...", retryAfter)
				return retryAfter, nil
			}
			return 0, err
		}).
		Until(ctx, func(l *coordinationv1.Lease) (done bool, err error) {
			switch {
			case l.Spec.HolderIdentity == nil || *l.Spec.HolderIdentity == "":
				log.Debugf("Autopilot controller lease doesn't have a holder identity (%s), continue waiting", l.ResourceVersion)
				return false, nil
			case kubernetes.IsValidLease(&l.Spec):
				log.Debugf("Autopilot controller lease is valid (%s)", l.ResourceVersion)
				return true, nil
			default:
				log.Debugf("Autopilot controller lease is invalid (%s), continue waiting", l.ResourceVersion)
				return false, nil
			}
		})
}
