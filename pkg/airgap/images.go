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

package airgap

import (
	"github.com/k0sproject/k0s/pkg/apis/k0s.k0sproject.io/v1beta1"
	"github.com/k0sproject/k0s/pkg/constant"
)

// GetImageURIs returns all image tags
func GetImageURIs(spec *v1beta1.ClusterSpec) []string {
	pauseImage := v1beta1.ImageSpec{
		Image:   constant.KubePauseContainerImage,
		Version: constant.KubePauseContainerImageVersion,
	}

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
		pauseImage.URI(),
	}

	if spec.Network != nil && spec.Network.NodeLocalLoadBalancer.IsEnabled() {
		nllbSpec := spec.Network.NodeLocalLoadBalancer
		switch nllbSpec.Type {
		case v1beta1.NllbTypeEnvoyProxy:
			if nllbSpec.EnvoyProxy != nil && nllbSpec.EnvoyProxy.Image != nil {
				imageURIs = append(imageURIs, nllbSpec.EnvoyProxy.Image.URI())
			} else {
				imageURIs = append(imageURIs, v1beta1.DefaultEnvoyProxyImage(spec.Images).URI())
			}
		}
	}

	return imageURIs
}
