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

package applier

import (
	"context"
	"fmt"
	"path"
	"path/filepath"

	"github.com/k0sproject/k0s/pkg/kubernetes"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/cli-runtime/pkg/resource"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"

	"github.com/sirupsen/logrus"
)

// manifestFilePattern is the glob pattern that all applicable manifest files need to match.
const manifestFilePattern = "*.yaml"

// Applier manages all the "static" manifests and applies them on the k8s API
type Applier struct {
	Name string
	Dir  string

	log             *logrus.Entry
	clientFactory   kubernetes.ClientFactoryInterface
	client          dynamic.Interface
	discoveryClient discovery.CachedDiscoveryInterface

	restClientGetter resource.RESTClientGetter
}

// NewApplier creates new Applier
func NewApplier(dir string, kubeClientFactory kubernetes.ClientFactoryInterface) Applier {
	name := filepath.Base(dir)
	log := logrus.WithFields(logrus.Fields{
		"component": "applier",
		"bundle":    name,
	})

	clientGetter := kubernetes.NewRESTClientGetter(kubeClientFactory)

	return Applier{
		log:              log,
		Dir:              dir,
		Name:             name,
		clientFactory:    kubeClientFactory,
		restClientGetter: clientGetter,
	}
}

func (a *Applier) lazyInit() error {
	if a.client == nil {
		c, err := a.clientFactory.GetDynamicClient()
		if err != nil {
			return err
		}

		a.client = c
	}

	if a.discoveryClient == nil {
		c, err := a.clientFactory.GetDiscoveryClient()
		if err != nil {
			return err
		}

		a.discoveryClient = c
	}

	return nil
}

// Apply resources
func (a *Applier) Apply(ctx context.Context) error {
	err := a.lazyInit()
	if err != nil {
		return fmt.Errorf("failed to initialize: %w", err)
	}

	files, err := filepath.Glob(path.Join(a.Dir, manifestFilePattern))
	if err != nil {
		return fmt.Errorf("while searching for files: %w", err)
	}

	resources, err := a.parseFiles(files)
	if err != nil {
		return fmt.Errorf("while parsing files: %w", err)
	}
	stack := Stack{
		Name:      a.Name,
		Resources: resources,
		Client:    a.client,
		Discovery: a.discoveryClient,
	}
	a.log.Debug("applying stack")
	if err = stack.Apply(ctx, true); err != nil {
		a.discoveryClient.Invalidate()
		return fmt.Errorf("failed to apply stack: %w", err)
	}
	a.log.Debug("successfully applied stack")

	return nil
}

// Delete deletes the entire stack by applying it with empty set of resources
func (a *Applier) Delete(ctx context.Context) error {
	err := a.lazyInit()
	if err != nil {
		return err
	}
	stack := Stack{
		Name:      a.Name,
		Client:    a.client,
		Discovery: a.discoveryClient,
	}
	logrus.Debugf("about to delete a stack %s with empty apply", a.Name)
	err = stack.Apply(ctx, true)
	return err
}

func (a *Applier) parseFiles(files []string) ([]*unstructured.Unstructured, error) {
	if len(files) == 0 {
		return nil, nil
	}

	objects, err := resource.NewBuilder(a.restClientGetter).
		Unstructured().
		Path(false, files...).
		Flatten().
		Do().
		Infos()
	if err != nil {
		return nil, err
	}

	var resources []*unstructured.Unstructured
	for _, o := range objects {
		item := o.Object.(*unstructured.Unstructured)
		if item.GetAPIVersion() != "" && item.GetKind() != "" {
			resources = append(resources, item)
		}
	}

	return resources, nil
}
