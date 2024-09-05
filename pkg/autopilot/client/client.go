// Copyright 2021 k0s authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package client

import (
	"sync"

	k0sclientset "github.com/k0sproject/k0s/pkg/client/clientset"
	apiextensionsclientset "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	extclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset/typed/apiextensions/v1"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// FactoryInterface is a collection of kubernetes clientset interfaces.
type FactoryInterface interface {
	GetClient() (kubernetes.Interface, error)
	GetK0sClient() (k0sclientset.Interface, error)
	GetAPIExtensionsClient() (apiextensionsclientset.Interface, error)
	GetExtensionClient() (extclient.ApiextensionsV1Interface, error) // Deprecated: Use [FactoryInterface.GetAPIExtensionsClient] instead.
	RESTConfig() *rest.Config
}

type clientFactory struct {
	client           kubernetes.Interface
	clientK0s        k0sclientset.Interface
	clientExtensions apiextensionsclientset.Interface
	restConfig       *rest.Config

	mutex sync.Mutex
}

var _ FactoryInterface = (*clientFactory)(nil)

func NewClientFactory(config *rest.Config) (FactoryInterface, error) {
	return &clientFactory{restConfig: config}, nil
}

// GetClient returns the core kubernetes clientset
func (cf *clientFactory) GetClient() (kubernetes.Interface, error) {
	cf.mutex.Lock()
	defer cf.mutex.Unlock()
	var err error

	if cf.client != nil {
		return cf.client, nil
	}

	client, err := kubernetes.NewForConfig(cf.restConfig)
	if err != nil {
		return nil, err
	}

	cf.client = client

	return cf.client, nil
}

// GetK0sClient returns the clientset for k0s
func (cf *clientFactory) GetK0sClient() (k0sclientset.Interface, error) {
	cf.mutex.Lock()
	defer cf.mutex.Unlock()
	var err error

	if cf.clientK0s != nil {
		return cf.clientK0s, nil
	}

	client, err := k0sclientset.NewForConfig(cf.restConfig)
	if err != nil {
		return nil, err
	}

	cf.clientK0s = client

	return cf.clientK0s, nil
}

// GetAPIExtensionsClient returns the clientset for API server extensions.
func (cf *clientFactory) GetAPIExtensionsClient() (apiextensionsclientset.Interface, error) {
	cf.mutex.Lock()
	defer cf.mutex.Unlock()
	var err error

	if cf.clientExtensions != nil {
		return cf.clientExtensions, nil
	}

	client, err := apiextensionsclientset.NewForConfig(cf.restConfig)
	if err != nil {
		return nil, err
	}

	cf.clientExtensions = client

	return client, nil
}

// Deprecated: Use [clientFactory.GetAPIExtensionsClient] instead.
func (cf *clientFactory) GetExtensionClient() (extclient.ApiextensionsV1Interface, error) {
	client, err := cf.GetAPIExtensionsClient()
	if err != nil {
		return nil, err
	}

	return client.ApiextensionsV1(), nil
}

func (cf *clientFactory) RESTConfig() *rest.Config {
	return cf.restConfig
}
