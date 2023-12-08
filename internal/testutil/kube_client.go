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

package testutil

import (
	"errors"
	"strings"

	k0sv1beta1 "github.com/k0sproject/k0s/pkg/client/clientset/typed/k0s/v1beta1"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	discoveryfake "k8s.io/client-go/discovery/fake"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	kubernetesscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	restfake "k8s.io/client-go/rest/fake"
	kubetesting "k8s.io/client-go/testing"

	"golang.org/x/exp/slices"
)

// NewFakeClientFactory creates new client factory which uses internally only the kube fake client interface
func NewFakeClientFactory(objects ...runtime.Object) FakeClientFactory {
	scheme := kubernetesscheme.Scheme

	fakeDiscovery := newFakeDiscovery(scheme)

	return FakeClientFactory{
		Client:          fake.NewSimpleClientset(objects...),
		DynamicClient:   dynamicfake.NewSimpleDynamicClient(scheme),
		DiscoveryClient: memory.NewMemCacheClient(fakeDiscovery),
		RawDiscovery:    fakeDiscovery,
		RESTClient:      &restfake.RESTClient{},
	}
}

func newFakeDiscovery(scheme *runtime.Scheme) *discoveryfake.FakeDiscovery {
	f := &discoveryfake.FakeDiscovery{Fake: &kubetesting.Fake{}}

	for gvk := range scheme.AllKnownTypes() {
		if !strings.HasPrefix(gvk.Version, "v") {
			continue // there are some "__internal" versions
		}

		var resources *metav1.APIResourceList
		if idx := slices.IndexFunc(f.Resources, func(l *metav1.APIResourceList) bool {
			return l.GroupVersion == gvk.GroupVersion().String()
		}); idx < 0 {
			resources = &metav1.APIResourceList{}
			f.Resources = append(f.Resources, resources)
		} else {
			resources = f.Resources[idx]
		}

		plural, singular := meta.UnsafeGuessKindToResource(gvk)
		resource := metav1.APIResource{
			Name:         plural.Resource,
			SingularName: singular.Resource,
			Namespaced:   true, // FIXME is there any way to figure this out reliably?
			Group:        gvk.Group,
			Version:      gvk.Version,
			Kind:         gvk.Kind,
		}

		// Some duct tape for guessing cluster resources
		switch {
		case strings.Contains(gvk.Kind, "Node"),
			strings.Contains(gvk.Kind, "Cluster"):
			resource.Namespaced = false
		}

		resources.APIResources = append(resources.APIResources, resource)
	}

	return f
}

type FakeClientFactory struct {
	Client          kubernetes.Interface
	DynamicClient   dynamic.Interface
	DiscoveryClient discovery.CachedDiscoveryInterface
	RawDiscovery    *discoveryfake.FakeDiscovery
	RESTClient      rest.Interface
}

func (f FakeClientFactory) GetClient() (kubernetes.Interface, error) {
	return f.Client, nil
}

func (f FakeClientFactory) GetDynamicClient() (dynamic.Interface, error) {
	return f.DynamicClient, nil
}

func (f FakeClientFactory) GetDiscoveryClient() (discovery.CachedDiscoveryInterface, error) {
	return f.DiscoveryClient, nil
}

func (f FakeClientFactory) GetConfigClient() (k0sv1beta1.ClusterConfigInterface, error) {
	return nil, errors.New("NOT IMPLEMENTED")
}

func (f FakeClientFactory) GetRESTClient() (rest.Interface, error) {
	return f.RESTClient, nil
}
func (f FakeClientFactory) GetRESTConfig() *rest.Config {
	return &rest.Config{}
}
