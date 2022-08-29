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

package workerconfig

import (
	"fmt"
	"strings"

	"github.com/k0sproject/k0s/pkg/apis/k0s.k0sproject.io/v1beta1"
	"github.com/k0sproject/k0s/pkg/applier"
	"github.com/k0sproject/k0s/pkg/constant"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kubeletv1beta1 "k8s.io/kubelet/config/v1beta1"

	"sigs.k8s.io/yaml"
)

// workerConfig represents the worker config for a given profile.
type workerConfig struct {
	apiServers             apiServers
	defaultImagePullPolicy corev1.PullPolicy
	envoyProxyImage        v1beta1.ImageSpec
	kubelet                kubeletv1beta1.KubeletConfiguration
}

type hostPort struct {
	host string
	port uint16
}

type apiServers []hostPort

func (c *workerConfig) toConfigMap(name string) (*corev1.ConfigMap, error) {
	kubelet, err := yaml.Marshal(c.kubelet)
	if err != nil {
		return nil, err
	}

	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("kubelet-config-%s-%s", name, constant.KubernetesMajorMinorVersion),
			Namespace: "kube-system",
			Labels: applier.
				CommonLabels(constant.WorkerConfigComponentName).
				With("k0s.k0sproject.io/worker-profile", name),
		},
		Data: map[string]string{
			"apiServers":             c.apiServers.String(),
			"envoyProxyImage":        c.envoyProxyImage.URI(),
			"defaultImagePullPolicy": string(c.defaultImagePullPolicy),
			"kubelet":                string(kubelet),
		},
	}, nil
}

func (hp *hostPort) String() string {
	return fmt.Sprintf("%s:%d", hp.host, hp.port)
}

// func parseAPIServer(hostPort string) (apiServer, error) {
// 	split := strings.SplitN(hostPort, ":", 2)
// 	if len(split) != 2 {
// 		return apiServer{}, fmt.Errorf("invalid format, expected <host>:<port>, got: %q", hostPort)
// 	}
// 	if net.ParseIP(split[0]) == nil && len(validation.IsDNS1123Subdomain(split[0])) > 0 {
// 		return apiServer{}, fmt.Errorf("host is neither an IP nor a domain name: %q", hostPort)
// 	}
// 	port, err := strconv.ParseUint(split[1], 10, 16)
// 	if err != nil {
// 		return apiServer{}, fmt.Errorf("invalid port number (%w): %q", err, hostPort)
// 	}

// 	return apiServer{split[0], uint16(port)}, nil
// }

func (s apiServers) String() string {
	var str strings.Builder
	for pos, hp := range s {
		if pos > 0 {
			str.WriteRune(',')
		}
		str.WriteString(hp.String())
	}

	return str.String()
}

func (s apiServers) DeepCopy() apiServers {
	if s == nil {
		return nil
	}

	return append((apiServers)(nil), s...)
}
