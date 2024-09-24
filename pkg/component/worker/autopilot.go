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

package worker

import (
	"context"
	"fmt"
	"slices"
	"time"

	autopilotv1beta2 "github.com/k0sproject/k0s/pkg/apis/autopilot/v1beta2"
	apcli "github.com/k0sproject/k0s/pkg/autopilot/client"
	apcont "github.com/k0sproject/k0s/pkg/autopilot/controller"
	aproot "github.com/k0sproject/k0s/pkg/autopilot/controller/root"
	k0sscheme "github.com/k0sproject/k0s/pkg/client/clientset/scheme"
	"github.com/k0sproject/k0s/pkg/component/manager"
	"github.com/k0sproject/k0s/pkg/config"
	"github.com/k0sproject/k0s/pkg/kubernetes/watch"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/sirupsen/logrus"
)

const (
	defaultPollDuration = 5 * time.Second
	defaultPollTimeout  = 5 * time.Minute
)

var _ manager.Component = (*Autopilot)(nil)

type Autopilot struct {
	K0sVars     *config.CfgVars
	CertManager *CertificateManager
}

func (a *Autopilot) Init(ctx context.Context) error {
	return nil
}

func (a *Autopilot) Start(ctx context.Context) error {
	log := logrus.WithFields(logrus.Fields{"component": "autopilot"})

	var (
		clientFactory apcli.FactoryInterface
		waitErr       error
	)
	if pollErr := wait.PollUntilContextTimeout(ctx, defaultPollDuration, defaultPollTimeout, true, func(ctx context.Context) (bool, error) {
		clientFactory, waitErr = a.newClientFactory(ctx)
		if waitErr == nil {
			return true, nil
		}

		log.WithError(waitErr).Debug("Failed to create autopilot client factory, retrying in ", defaultPollDuration)
		return false, nil
	}); pollErr != nil {
		if waitErr == nil {
			return pollErr
		}

		return fmt.Errorf("%w: %w", pollErr, waitErr)
	}

	log.Info("Autopilot client factory created, booting up worker root controller")
	autopilotRoot, err := apcont.NewRootWorker(aproot.RootConfig{
		KubeConfig:          a.K0sVars.KubeletAuthConfigPath,
		K0sDataDir:          a.K0sVars.DataDir,
		Mode:                "worker",
		ManagerPort:         8899,
		MetricsBindAddr:     "0",
		HealthProbeBindAddr: "0",
	}, log, clientFactory)
	if err != nil {
		return fmt.Errorf("failed to create autopilot worker: %w", err)
	}

	go func() {
		if err := autopilotRoot.Run(ctx); err != nil {
			logrus.WithError(err).Error("Error running autopilot")

			// TODO: We now have a service with nothing running.. now what?
		}
	}()

	return nil
}

// Stop stops Autopilot
func (a *Autopilot) Stop() error {
	return nil
}

func (a *Autopilot) newClientFactory(ctx context.Context) (apcli.FactoryInterface, error) {
	restConfig, err := a.CertManager.GetRestConfig(ctx)
	if err != nil {
		return nil, err
	}

	clientFactory, err := apcli.NewClientFactory(restConfig)
	if err != nil {
		return nil, err
	}

	// We need to wait until all autopilot CRDs are established.
	// Gather all kinds in the autopilot API group.
	var kinds []string
	gv := autopilotv1beta2.SchemeGroupVersion
	for kind := range k0sscheme.Scheme.KnownTypes(gv) {
		// For some reason, the scheme also returns types from core/v1. Filter
		// those out by only adding kinds which are _only_ in the autopilot
		// group, and not in some other group as well. The only way to get all
		// the GVKs for a certain type is by creating a new instance of that
		// type and then asking the scheme about it.
		obj, err := k0sscheme.Scheme.New(gv.WithKind(kind))
		if err != nil {
			return nil, err
		}
		gvks, _, err := k0sscheme.Scheme.ObjectKinds(obj)
		if err != nil {
			return nil, err
		}

		// Skip the kind if there's at least one GVK which is not in the
		// autopilot group
		if !slices.ContainsFunc(gvks, func(gvk schema.GroupVersionKind) bool {
			return gvk.Group != autopilotv1beta2.GroupName
		}) {
			kinds = append(kinds, kind)
		}
	}

	client, err := clientFactory.GetExtensionClient()
	if err != nil {
		return nil, err
	}

	// Watch all the CRDs until all the required ones are established.
	log := logrus.WithField("component", "autopilot")
	slices.Sort(kinds) // for cosmetic purposes
	if err = watch.CRDs(client.CustomResourceDefinitions()).
		WithErrorCallback(func(err error) (time.Duration, error) {
			if retryAfter, e := watch.IsRetryable(err); e == nil {
				log.WithError(err).Info(
					"Transient error while watching for CRDs",
					", starting over after ", retryAfter, " ...",
				)
				return retryAfter, nil
			}

			retryAfter := 10 * time.Second
			log.WithError(err).Error(
				"Error while watching CRDs",
				", starting over after ", retryAfter, " ...",
			)
			return retryAfter, nil
		}).
		Until(ctx, func(item *apiextensionsv1.CustomResourceDefinition) (bool, error) {
			if item.Spec.Group != autopilotv1beta2.GroupName {
				return false, nil // Not an autopilot CRD.
			}

			// Find the established status for the CRD.
			var established apiextensionsv1.ConditionStatus
			for _, cond := range item.Status.Conditions {
				if cond.Type == apiextensionsv1.Established {
					established = cond.Status
					break
				}
			}

			if established != apiextensionsv1.ConditionTrue {
				return false, nil // CRD not yet established.
			}

			// Remove the CRD's (list) kind from the list.
			kinds = slices.DeleteFunc(kinds, func(kind string) bool {
				return kind == item.Spec.Names.Kind || kind == item.Spec.Names.ListKind
			})

			// If the list is empty, all required CRDs are established.
			return len(kinds) < 1, nil
		}); err != nil {
		return nil, fmt.Errorf("while waiting for Autopilot CRDs %v to become established: %w", kinds, err)
	}

	return clientFactory, nil
}
