// SPDX-FileCopyrightText: 2021 k0s authors
// SPDX-License-Identifier: Apache-2.0

package kubernetes

import (
	"context"
	"strings"
	"time"

	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// IsValidLease check whether or not the lease is expired
func IsValidLease(leaseSpec *coordinationv1.LeaseSpec) bool {
	if leaseSpec.HolderIdentity == nil || *leaseSpec.HolderIdentity == "" {
		return false
	}
	if leaseSpec.RenewTime == nil || leaseSpec.LeaseDurationSeconds == nil {
		return false
	}

	leaseDuration := time.Duration(*leaseSpec.LeaseDurationSeconds) * time.Second
	return leaseSpec.RenewTime.Add(leaseDuration).After(time.Now())
}

func CountActiveControllerLeases(ctx context.Context, kubeClient kubernetes.Interface) (count uint, _ error) {
	leases, err := kubeClient.CoordinationV1().Leases(corev1.NamespaceNodeLease).List(ctx, metav1.ListOptions{})
	if err != nil {
		return 0, err
	}
	for _, l := range leases.Items {
		if strings.HasPrefix(l.Name, "k0s-ctrl-") && IsValidLease(&l.Spec) {
			count++
		}
	}

	return count, nil
}
