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

package clusterconfig

import (
	"context"
	"os"
	"time"

	"github.com/k0sproject/k0s/pkg/apis/k0s.k0sproject.io/v1beta1"
	"github.com/k0sproject/k0s/pkg/component"
	"github.com/k0sproject/k0s/pkg/constant"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/sirupsen/logrus"
)

type reconcilingEventReporter struct {
	log logrus.FieldLogger

	kubeClient kubernetes.Interface
	receiver   component.Reconcilable
}

// NewReconcilingEventReporter reconciles its receiver and reports the outcome
// as a ConfigReconciling Kubernetes event.
func NewReconcilingEventReporter(kubeClient kubernetes.Interface, receiver component.Reconcilable) component.Reconcilable {
	return &reconcilingEventReporter{
		log: logrus.WithFields(logrus.Fields{"component": "reconciling_event_reporter"}),

		kubeClient: kubeClient,
		receiver:   receiver,
	}
}

func (r *reconcilingEventReporter) Reconcile(ctx context.Context, config *v1beta1.ClusterConfig) error {
	err := r.receiver.Reconcile(ctx, config)

	timeout, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	r.reportStatus(timeout, config, err)
	return err
}

func (r *reconcilingEventReporter) reportStatus(ctx context.Context, config *v1beta1.ClusterConfig, reconcileError error) {
	hostname, err := os.Hostname()
	if err != nil {
		r.log.WithError(err).Warn("Failed to get hostname")
		hostname = ""
	}

	// TODO We need to design proper status field(s) to the cluster cfg object, now just send event
	now := time.Now()
	e := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "k0s.",
		},
		EventTime:      metav1.MicroTime{now},
		FirstTimestamp: metav1.Time{now},
		LastTimestamp:  metav1.Time{now},
		InvolvedObject: corev1.ObjectReference{
			Kind:            v1beta1.ClusterConfigKind,
			Namespace:       config.Namespace,
			Name:            config.Name,
			UID:             config.UID,
			APIVersion:      v1beta1.ClusterConfigAPIVersion,
			ResourceVersion: config.ResourceVersion,
		},
		Action:              "ConfigReconciling",
		ReportingController: "k0s-controller",
		ReportingInstance:   hostname,
	}
	if reconcileError != nil {
		e.Reason = "FailedReconciling"
		e.Message = reconcileError.Error()
		e.Type = corev1.EventTypeWarning
	} else {
		e.Reason = "SuccessfulReconcile"
		e.Message = "Successfully reconciled cluster config"
		e.Type = corev1.EventTypeNormal
	}
	_, err = r.kubeClient.CoreV1().Events(constant.ClusterConfigNamespace).Create(ctx, e, metav1.CreateOptions{})
	if err != nil {
		r.log.WithError(err).Errorf("Failed to create ConfigReconciling event (%s: %s)", e.Reason, e.Message)
	}
}
