// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package helm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"github.com/sirupsen/logrus"
	helmkube "helm.sh/helm/v3/pkg/kube"
)

type client struct {
	*helmkube.Client
	ctx context.Context
	log logrus.FieldLogger
}

func (c *client) Wait(resources helmkube.ResourceList, timeout time.Duration) error {
	return c.waitForResources(resources, timeout, helmkube.PausedAsReady(true))
}

func (c *client) WaitWithJobs(resources helmkube.ResourceList, timeout time.Duration) error {
	return c.waitForResources(resources, timeout, helmkube.PausedAsReady(true), helmkube.CheckJobs(true))
}

func (c *client) waitForResources(resources helmkube.ResourceList, timeout time.Duration, opts ...helmkube.ReadyCheckerOption) error {
	clients, err := c.Factory.KubernetesClientSet()
	if err != nil {
		return err
	}

	checker := helmkube.NewReadyChecker(clients, c.Log, opts...)

	ctx, cancel := context.WithTimeout(c.ctx, timeout)
	defer cancel()

	timer := time.NewTimer(0)
	defer timer.Stop()

checkAllResources:
	for {
		for _, resource := range resources {
			if ready, err := checker.IsReady(ctx, resource); err != nil {
				var stop bool
				select {
				case <-ctx.Done():
					stop = true
				default:
					var status apierrors.APIStatus
					if errors.As(err, &status) {
						code := status.Status().Code
						stop = code != 0 && code != http.StatusTooManyRequests && (code < 500 || code == http.StatusNotImplemented)
					}
				}

				resourceDesc := resource.ObjectName()
				if resource.Namespaced() {
					resourceDesc += " in namespace " + resource.Namespace
				}

				if stop {
					return fmt.Errorf("while checking for readiness of %s: %w", resourceDesc, err)
				}

				c.log.WithError(err).Debug("While checking for readiness of ", resourceDesc)
			} else if ready {
				continue
			}

			timer.Reset(2 * time.Second)

			select {
			case <-timer.C:
				continue checkAllResources
			case <-ctx.Done():
				return context.Cause(ctx)
			}
		}

		return nil
	}
}
