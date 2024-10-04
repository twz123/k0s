/*
Copyright 2021 k0s authors

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

package clusterconfig

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/k0sproject/k0s/internal/sync/value"
	"github.com/k0sproject/k0s/pkg/apis/k0s/v1beta1"
	autopilotcommon "github.com/k0sproject/k0s/pkg/autopilot/common"
	k0sclient "github.com/k0sproject/k0s/pkg/client/clientset/typed/k0s/v1beta1"
	"github.com/k0sproject/k0s/pkg/constant"
	kubeutil "github.com/k0sproject/k0s/pkg/kubernetes"
	"github.com/k0sproject/k0s/pkg/kubernetes/watch"

	corev1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/events"

	"github.com/sirupsen/logrus"
)

type APIConfigSource struct {
	nodeName     string
	configClient k0sclient.ClusterConfigInterface
	eventSink    events.EventSink
	current      value.Latest[*v1beta1.ClusterConfig]
	stop         func()
}

func NewAPIConfigSource(kubeClientFactory kubeutil.ClientFactoryInterface) (*APIConfigSource, error) {
	nodeName, err := autopilotcommon.FindEffectiveHostname()
	if err != nil {
		return nil, err
	}
	client, err := kubeClientFactory.GetClient()
	if err != nil {
		return nil, err
	}
	k0sClient, err := kubeClientFactory.GetK0sClient()
	if err != nil {
		return nil, err
	}

	a := &APIConfigSource{
		nodeName:     nodeName,
		configClient: k0sClient.K0sV1beta1().ClusterConfigs(constant.ClusterConfigNamespace),
		eventSink:    &events.EventSinkImpl{Interface: client.EventsV1()},
	}
	return a, nil
}

// CurrentConfig implements [Source].
func (a *APIConfigSource) CurrentConfig() (current *v1beta1.ClusterConfig, expired <-chan struct{}) {
	return a.current.Peek()
}

// Init implements [manager.Component].
func (*APIConfigSource) Init(context.Context) error { return nil }

// Start implements [manager.Component].
func (a *APIConfigSource) Start(context.Context) error {
	var lastObservedVersion string

	log := logrus.WithField("component", "clusterconfig.apiConfigSource")
	watch := watch.ClusterConfigs(a.configClient).
		WithObjectName(constant.ClusterConfigObjectName).
		WithErrorCallback(func(err error) (time.Duration, error) {
			if retryAfter, e := watch.IsRetryable(err); e == nil {
				log.WithError(err).Infof(
					"Transient error while watching for updates to cluster configuration"+
						", last observed version is %q"+
						", starting over after %s ...",
					lastObservedVersion, retryAfter,
				)
				return retryAfter, nil
			}

			retryAfter := 10 * time.Second
			log.WithError(err).Errorf(
				"Failed to watch for updates to cluster configuration"+
					", last observed version is %q"+
					", starting over after %s ...",
				lastObservedVersion, retryAfter,
			)
			return retryAfter, nil
		})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	a.stop = func() { cancel(); <-done }

	go func() {
		defer close(done)
		_ = watch.Until(ctx, func(cfg *v1beta1.ClusterConfig) (bool, error) {
			// Push changes only when the config actually changes
			if lastObservedVersion == cfg.ResourceVersion {
				return false, nil
			}

			lastObservedVersion = cfg.ResourceVersion
			if err := errors.Join(cfg.Validate()...); err != nil {
				log.WithError(err).Errorf("Failed to validate cluster configuration (resource version %q)", cfg.ResourceVersion)
				a.reportValidationFailure(ctx, log, cfg, err.Error())
				return false, nil
			}

			log.Debugf("Cluster configuration update to resource version %q", cfg.ResourceVersion)
			a.current.Set(cfg)
			return false, nil
		})
	}()

	return nil
}

// Stop implements [manager.Component].
func (a *APIConfigSource) Stop() error {
	a.stop()
	return nil
}

func (a *APIConfigSource) reportValidationFailure(ctx context.Context, log logrus.FieldLogger, config *v1beta1.ClusterConfig, note string) {
	now := time.Now()

	if _, err := a.eventSink.Create(ctx, &eventsv1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%v.%x", config.Name, now.UnixNano()),
			Namespace: config.Namespace,
		},
		EventTime:           metav1.MicroTime{Time: now},
		ReportingController: v1beta1.GroupName + "/controller",
		ReportingInstance:   a.nodeName,
		Action:              "Validation",
		Reason:              "Cluster configuration reconciliation",
		Regarding: corev1.ObjectReference{
			APIVersion:      config.APIVersion,
			Kind:            config.Kind,
			Name:            config.Name,
			Namespace:       config.Namespace,
			UID:             config.UID,
			ResourceVersion: config.ResourceVersion,
		},
		Note: note,
		Type: corev1.EventTypeWarning,
	}); err != nil {
		log.WithError(err).Warnf(
			"Failed to create event for ClusterConfig validation error (resource version %q)",
			config.ResourceVersion,
		)
	}
}
