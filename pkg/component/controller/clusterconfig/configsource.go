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
	"github.com/k0sproject/k0s/pkg/apis/k0s/v1beta1"
)

type Source interface {
	// Retrieves the current cluster configuration and its associated expiration
	// channel. Whenever an updated configuration is observed, the expiration
	// channel is closed. Callers can use this to be notified of updates.
	CurrentConfig() (current *v1beta1.ClusterConfig, expired <-chan struct{})
}
