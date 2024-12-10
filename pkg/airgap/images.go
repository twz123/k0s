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

package airgap

import (
	"iter"
	"runtime"

	"github.com/k0sproject/k0s/pkg/apis/k0s/v1beta1"
	"github.com/k0sproject/k0s/pkg/constant"
)

// Yields all images in spec needed for airgapped installations.
func ImagesInSpec(spec *v1beta1.ClusterSpec, all bool) iter.Seq[v1beta1.ImageSpec] {
	return func(yield func(v1beta1.ImageSpec) bool) {
		for _, i := range []*v1beta1.ImageSpec{
			spec.Images.Calico.CNI,
			spec.Images.Calico.KubeControllers,
			spec.Images.Calico.Node,
			spec.Images.CoreDNS,
			spec.Images.Konnectivity,
			spec.Images.KubeProxy,
			spec.Images.KubeRouter.CNI,
			spec.Images.KubeRouter.CNIInstaller,
			spec.Images.MetricsServer,
		} {
			if i != nil && !yield(*i) {
				return
			}
		}

		if !yield(v1beta1.ImageSpec{
			Image:   constant.KubePauseContainerImage,
			Version: constant.KubePauseContainerImageVersion,
		}) {
			return
		}

		if spec.Network != nil {
			nllb := spec.Network.NodeLocalLoadBalancing
			if nllb != nil && (all || nllb.IsEnabled()) {
				switch nllb.Type {
				case v1beta1.NllbTypeEnvoyProxy:
					if runtime.GOARCH != "arm" && nllb.EnvoyProxy != nil && nllb.EnvoyProxy.Image != nil {
						if !yield(*nllb.EnvoyProxy.Image) {
							return
						}
					}
				}
			}
		}
	}
}
