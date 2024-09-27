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

package controller

import (
	"bytes"
	"context"
	_ "embed"
	"path"
	"path/filepath"

	"github.com/k0sproject/k0s/internal/pkg/dir"
	"github.com/k0sproject/k0s/internal/pkg/file"
	"github.com/k0sproject/k0s/pkg/applier"
	"github.com/k0sproject/k0s/pkg/component/manager"
	"github.com/k0sproject/k0s/pkg/kubernetes"
	"github.com/sirupsen/logrus"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/cli-runtime/pkg/resource"
)

const SystemRBACStackName = "bootstraprbac"

// SystemRBAC implements system RBAC reconciler
type SystemRBAC struct {
	ManifestsDir string
	Clients      kubernetes.ClientFactoryInterface
}

var _ manager.Component = (*SystemRBAC)(nil)

// Writes an ignore notice to the old stack directory if it exists.
func (s *SystemRBAC) Init(context.Context) error {
	stackDir := path.Join(s.ManifestsDir, SystemRBACStackName)
	if exists, err := dir.Exists(stackDir); err != nil {
		return err
	} else if !exists {
		return nil // nothing to do
	}

	if err := file.AtomicWithTarget(filepath.Join(stackDir, "ignored.txt")).WriteString(
		"The " + SystemRBACStackName + " stack is handled internally since k0s v1.32." +
			"\nThis directory is ignored and can be safely removed.",
	); err != nil {
		logrus.WithField("component", "system-rbac").WithError(err).Warn("Failed to write ignore notice")
	}

	return nil
}

// Writes the bootstrap RBAC manifests into the manifests folder.
func (s *SystemRBAC) Start(ctx context.Context) error {
	infos, err := resource.NewLocalBuilder().
		Unstructured().
		Stream(bytes.NewReader(systemRBAC), SystemRBACStackName).
		Flatten().
		Do().
		Infos()
	if err != nil {
		return err
	}

	resources := make([]*unstructured.Unstructured, len(infos))
	for i := range infos {
		resources[i] = infos[i].Object.(*unstructured.Unstructured)
	}

	stack := applier.Stack{
		Name:      SystemRBACStackName,
		Resources: resources,
		Clients:   s.Clients,
	}

	return stack.Apply(ctx, true)
}

// Stop implements [manager.Component].
func (s *SystemRBAC) Stop() error { return nil }

//go:embed systemrbac.yaml
var systemRBAC []byte
