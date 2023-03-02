/*
Copyright 2020 k0s authors

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

import (
	"github.com/k0sproject/k0s/pkg/apis/k0s"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var SchemeGroupVersion = schema.GroupVersion{Group: k0s.GroupName, Version: Version}

var (
	SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes, RegisterDefaults)
	AddToScheme   = SchemeBuilder.AddToScheme
)

func addKnownTypes(scheme *runtime.Scheme) error {
	// Add all kubebuilder:object:root=true types here.
	scheme.AddKnownTypes(SchemeGroupVersion,
		&ClusterConfig{},
		&ClusterConfigList{},
	)
	return nil
}
