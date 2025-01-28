/*
Copyright 2023 k0s authors

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

package node

import (
	"context"
	"fmt"

	apitypes "k8s.io/apimachinery/pkg/types"
	nodeutil "k8s.io/component-helpers/node/util"
)

// GetNodeName returns the node name for the node taking OS, cloud provider and override into account
func GetNodeName(override string) (apitypes.NodeName, error) {
	return getNodename(context.TODO(), override)
}

func getNodename(ctx context.Context, override string) (apitypes.NodeName, error) {
	if override == "" {
		var err error
		override, err = defaultNodenameOverride(ctx)
		if err != nil {
			return "", err
		}
	}
	nodeName, err := nodeutil.GetHostname(override)
	if err != nil {
		return "", fmt.Errorf("failed to determine node name: %w", err)
	}
	return apitypes.NodeName(nodeName), nil
}
