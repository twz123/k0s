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

package v1beta1

import "github.com/k0sproject/k0s/pkg/constant"

// SystemUsername represents an operating system user name.
// +kubebuilder:validation:MinLength=1
// +kubebuilder:validation:MaxLength=256
type SystemUsername string

// SystemUsers defines the usernames to use for each component.
type SystemUsers struct {
	// +optional
	Etcd SystemUsername `json:"etcdUser,omitempty"`
	// +optional
	Kine SystemUsername `json:"kineUser,omitempty"`
	// +optional
	Konnectivity SystemUsername `json:"konnectivityUser,omitempty"`
	// +optional
	KubeAPIServer SystemUsername `json:"kubeAPIserverUser,omitempty"`
	// +optional
	KubeScheduler SystemUsername `json:"kubeSchedulerUser,omitempty"`
}

// DefaultSystemUsers returns the default system users to be used for the different components
func DefaultSystemUsers() (users SystemUsers) {
	SetDefaults_SystemUsers(&users)
	return
}

// Or returns this username or default_ if this username is empty.
func (u SystemUsername) Or(default_ string) SystemUsername {
	if u == "" {
		return SystemUsername(default_)
	}
	return u
}

func SetDefaults_SystemUsers(u *SystemUsers) {
	u.Etcd = u.Etcd.Or(constant.EtcdUser)
	u.Kine = u.Kine.Or(constant.KineUser)
	u.Konnectivity = u.Konnectivity.Or(constant.KonnectivityServerUser)
	u.KubeAPIServer = u.KubeAPIServer.Or(constant.ApiserverUser)
	u.KubeScheduler = u.KubeScheduler.Or(constant.SchedulerUser)
}
