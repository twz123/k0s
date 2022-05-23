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

package controller

import (
	"path/filepath"

	"github.com/k0sproject/k0s/internal/pkg/file"
	"github.com/k0sproject/k0s/pkg/constant"
)

// NewManifestsSaver builds a new filesystem manifests saver.
func NewManifestsSaver(manifest string, dataDir string) (manifestsSaver, error) {
	manifestDir := filepath.Join(dataDir, "manifests", manifest)
	return file.NewFsDirSink(manifestDir, constant.ManifestsDirMode, 0644)
}
