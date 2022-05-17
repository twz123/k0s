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
	"errors"
	"fmt"
	"strings"

	"github.com/k0sproject/k0s/pkg/apis/k0s.k0sproject.io/v1beta1"
	"github.com/k0sproject/k0s/pkg/component"
)

// EnsureValid reconciles its receiver, forwarding only valid cluster
// configurations to it. Any invalid cluster configurations will be rejected by
// returning an error and not forwarded to the receiver.
func EnsureValid(receiver component.Reconcilable) component.ReconcileFn {
	return func(ctx context.Context, config *v1beta1.ClusterConfig) error {
		return ensureValid(ctx, config, receiver)
	}
}

func ensureValid(ctx context.Context, config *v1beta1.ClusterConfig, receiver component.Reconcilable) error {
	errs := config.Validate()
	switch len(errs) {
	case 0:
		return receiver.Reconcile(ctx, config)

	case 1:
		return fmt.Errorf("failed to validate config: %w", errs[0])

	default:
		var buf strings.Builder
		buf.WriteString("failed to validate config: ")
		for i, err := range errs {
			if i != 0 {
				buf.WriteString("; ")
			}
			buf.WriteString(err.Error())
		}
		return errors.New(buf.String())
	}
}
