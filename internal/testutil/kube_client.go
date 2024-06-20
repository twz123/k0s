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
	"reflect"
	"strings"

	"github.com/k0sproject/k0s/internal/testutil/fakeclient"
	k0sv1beta1 "github.com/k0sproject/k0s/pkg/client/clientset/typed/k0s/v1beta1"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	discoveryfake "k8s.io/client-go/discovery/fake"
	"k8s.io/client-go/dynamic"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes"
	kubernetesscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	clientgotesting "k8s.io/client-go/testing"

	"golang.org/x/exp/slices"
)

// NewFakeClientFactory creates new client factory which uses internally only the kube fake client interface
func NewFakeClientFactory(objects ...runtime.Object) *FakeClientFactory {
	scheme := kubernetesscheme.Scheme

	fakeDyn := dynamicfake.NewSimpleDynamicClient(scheme, objects...)
	fakeDiscovery := &discoveryfake.FakeDiscovery{Fake: &fakeDyn.Fake}
	addAPIResources(scheme, &fakeDiscovery.Resources)

	var convertingFake clientgotesting.Fake
	convertingFake.PrependReactor("*", "*", func(action clientgotesting.Action) (bool, runtime.Object, error) {
		ret, err := proxyTypedAction(fakeDyn, action)
		return true, ret, err
	})

	return &FakeClientFactory{
		Client:          &fakeclient.Kubernetes{Fake: &convertingFake, ObjectTracker: fakeDyn.Tracker()},
		DynamicClient:   fakeDyn,
		DiscoveryClient: memory.NewMemCacheClient(fakeDiscovery),
	}
}

func proxyTypedAction(fakeDyn *dynamicfake.FakeDynamicClient, action clientgotesting.Action) (runtime.Object, error) {
	obj, err := fakeDyn.Fake.Invokes(action, nil)
	if err == nil && obj != nil && ret != nil && !reflect.TypeOf(obj).AssignableTo(reflect.TypeOf(ret)) {
		if meta.IsListType(ret) {
			if u, ok := obj.(*unstructured.UnstructuredList); ok {
				copy := ret.DeepCopyObject()
				if runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, copy) != nil {
					return copy, err
				}
			}
		} else {
			if u, ok := obj.(*unstructured.Unstructured); ok {
				copy := ret.DeepCopyObject()
				if runtime.DefaultUnstructuredConverter.FromUnstructured(u.Object, copy) != nil {
					return copy, err
				}
			}
		}
	}

}

func addAPIResources(scheme *runtime.Scheme, allResoures *[]*metav1.APIResourceList) {
	for gvk := range scheme.AllKnownTypes() {
		if !strings.HasPrefix(gvk.Version, "v") {
			continue // there are some "__internal" versions
		}

		groupVersion := gvk.GroupVersion().String()

		var resources *metav1.APIResourceList
		if idx := slices.IndexFunc(*allResoures, func(l *metav1.APIResourceList) bool {
			return l.GroupVersion == groupVersion
		}); idx < 0 {
			resources = &metav1.APIResourceList{GroupVersion: groupVersion}
			*allResoures = append(*allResoures, resources)
		} else {
			resources = (*allResoures)[idx]
		}

		plural, singular := meta.UnsafeGuessKindToResource(gvk)
		resource := metav1.APIResource{
			Name:         plural.Resource,
			SingularName: singular.Resource,
			Group:        gvk.Group,
			Version:      gvk.Version,
			Kind:         gvk.Kind,
		}

		// Some duct tape for guessing cluster resources
		// FIXME is there any way to figure this out reliably?
		switch {
		case strings.Contains(gvk.Kind, "Node"),
			strings.Contains(gvk.Kind, "Namespace"),
			strings.Contains(gvk.Kind, "Cluster"):
			resource.Namespaced = false
		default:
			resource.Namespaced = true
		}

		resources.APIResources = append(resources.APIResources, resource)
	}
}

type FakeClientFactory struct {
	Client          kubernetes.Interface
	DynamicClient   dynamic.Interface
	DiscoveryClient discovery.CachedDiscoveryInterface
}

func (f *FakeClientFactory) GetClient() (kubernetes.Interface, error) {
	return f.Client, nil
}

func (f *FakeClientFactory) GetDynamicClient() (dynamic.Interface, error) {
	return f.DynamicClient, nil
}

func (f *FakeClientFactory) GetDiscoveryClient() (discovery.CachedDiscoveryInterface, error) {
	return f.DiscoveryClient, nil
}

func (f *FakeClientFactory) GetConfigClient() (k0sv1beta1.ClusterConfigInterface, error) {
	return nil, errors.New("NOT IMPLEMENTED")
}

func (f *FakeClientFactory) GetRESTClient() (rest.Interface, error) {
	return nil, errors.New("NOT IMPLEMENTED")
}

func (f FakeClientFactory) GetRESTConfig() *rest.Config {
	return &rest.Config{}
}
