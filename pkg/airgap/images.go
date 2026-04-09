// SPDX-FileCopyrightText: 2021 k0s authors
// SPDX-License-Identifier: Apache-2.0

package airgap

import (
	"github.com/k0sproject/k0s/pkg/apis/k0s/v1beta1"
	"github.com/k0sproject/k0s/pkg/component/worker/nllb"
)

// GetImageURIs returns all image tags
func GetImageURIs(spec *v1beta1.ClusterSpec, all bool) []string {

	imageURIs := []string{
		spec.Images.Calico.CNI.URI(),
		spec.Images.Calico.KubeControllers.URI(),
		spec.Images.Calico.Node.URI(),
		spec.Images.CoreDNS.URI(),
		spec.Images.Konnectivity.URI(),
		spec.Images.KubeProxy.URI(),
		spec.Images.KubeRouter.CNI.URI(),
		spec.Images.KubeRouter.CNIInstaller.URI(),
		spec.Images.MetricsServer.URI(),
		spec.Images.Pause.URI(),
	}

	if all {
		// Currently we can't determine if the user has enabled the PushGateway via
		// config so include it only if all is requested
		imageURIs = append(imageURIs,
			spec.Images.PushGateway.URI(),
		)
	}

	if spec.Network != nil {
		lb := spec.Network.NodeLocalLoadBalancing
		if lb != nil && (all || lb.IsEnabled()) {
			switch lb.Type {
			case v1beta1.NllbTypeEnvoyProxy:
				if nllb.EnvoySupported && lb.EnvoyProxy != nil && lb.EnvoyProxy.Image != nil {
					imageURIs = append(imageURIs, lb.EnvoyProxy.Image.URI())
				}
			}
		}
	}

	return imageURIs
}
