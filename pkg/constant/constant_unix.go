//go:build unix

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

package constant

const (
	// DataDirDefault is the default directory containing k0s state.
	DataDirDefault = "/var/lib/k0s"

	// KubeletVolumePluginDir defines the location for kubelet volume plugin executables.
	KubeletVolumePluginDir = "/usr/libexec/k0s/kubelet-plugins/volume/exec"

	KineSocket           = "kine/kine.sock:2379"
	K0sConfigPathDefault = "/etc/k0s/k0s.yaml"

	ExecutableSuffix = ""
)
