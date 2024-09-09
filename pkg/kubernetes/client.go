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

package kubernetes

import (
	"sync"
	"sync/atomic"

	k0sclientset "github.com/k0sproject/k0s/pkg/client/clientset"
	etcdMemberClient "github.com/k0sproject/k0s/pkg/client/clientset/typed/etcd/v1beta1"
	cfgClient "github.com/k0sproject/k0s/pkg/client/clientset/typed/k0s/v1beta1"
	"github.com/k0sproject/k0s/pkg/constant"

	apiextensionsclientset "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// ClientFactoryInterface defines a factory interface to load a kube client
type ClientFactoryInterface interface {
	GetClient() (kubernetes.Interface, error)
	GetDynamicClient() (dynamic.Interface, error)
	GetDiscoveryClient() (discovery.CachedDiscoveryInterface, error)
	GetAPIExtensionsClient() (apiextensionsclientset.Interface, error)
	GetK0sClient() (k0sclientset.Interface, error)
	GetConfigClient() (cfgClient.ClusterConfigInterface, error) // Deprecated: Use [ClientFactoryInterface.GetK0sClient] instead.
	GetRESTConfig() (*rest.Config, error)
	GetEtcdMemberClient() (etcdMemberClient.EtcdMemberInterface, error) // Deprecated: Use [ClientFactoryInterface.GetK0sClient] instead.
}

// ClientFactory implements a cached and lazy-loading ClientFactory for all the different types of kube clients we use
// It's implemented as lazy-loading so we can create the factory itself before we have the api, etcd and other components up so we can pass
// the factory itself to components needing kube clients and creation time.
type ClientFactory struct {
	LoadRESTConfig func() (*rest.Config, error)

	client              atomic.Pointer[kubernetes.Clientset]
	dynamicClient       atomic.Pointer[dynamic.DynamicClient]
	discoveryClient     atomic.Pointer[discovery.CachedDiscoveryInterface]
	apiExtensionsClient atomic.Pointer[apiextensionsclientset.Clientset]
	k0sClient           atomic.Pointer[k0sclientset.Clientset]
	restConfig          atomic.Pointer[rest.Config]

	mutex sync.Mutex
}

func (c *ClientFactory) GetClient() (kubernetes.Interface, error) {
	return lazyLoadClient(c, &c.client, kubernetes.NewForConfig)
}

func (c *ClientFactory) GetDynamicClient() (dynamic.Interface, error) {
	return lazyLoadClient(c, &c.dynamicClient, dynamic.NewForConfig)
}

func (c *ClientFactory) GetDiscoveryClient() (discovery.CachedDiscoveryInterface, error) {
	client, err := lazyLoadClient(c, &c.discoveryClient, func(c *rest.Config) (*discovery.CachedDiscoveryInterface, error) {
		discoveryClient, err := discovery.NewDiscoveryClientForConfig(c)
		if err != nil {
			return nil, err
		}
		cachedClient := memory.NewMemCacheClient(discoveryClient)
		return &cachedClient, nil
	})
	if err != nil {
		return nil, err
	}
	return *client, err
}

func (c *ClientFactory) GetAPIExtensionsClient() (apiextensionsclientset.Interface, error) {
	return lazyLoadClient(c, &c.apiExtensionsClient, apiextensionsclientset.NewForConfig)
}

func (c *ClientFactory) GetK0sClient() (k0sclientset.Interface, error) {
	return lazyLoadClient(c, &c.k0sClient, k0sclientset.NewForConfig)
}

// Deprecated: Use [clientFactory.GetK0sClient] instead.
func (c *ClientFactory) GetConfigClient() (cfgClient.ClusterConfigInterface, error) {
	k0sClient, err := c.GetK0sClient()
	if err != nil {
		return nil, err
	}

	return k0sClient.K0sV1beta1().ClusterConfigs(constant.ClusterConfigNamespace), nil
}

// Deprecated: Use [clientFactory.GetK0sClient] instead.
func (c *ClientFactory) GetEtcdMemberClient() (etcdMemberClient.EtcdMemberInterface, error) {
	k0sClient, err := c.GetK0sClient()
	if err != nil {
		return nil, err
	}

	return k0sClient.EtcdV1beta1().EtcdMembers(), nil
}

func (c *ClientFactory) GetRESTConfig() (*rest.Config, error) {
	return lazyLoad(c, &c.restConfig, c.LoadRESTConfig)
}

func lazyLoadClient[T any](cf *ClientFactory, ptr *atomic.Pointer[T], load func(*rest.Config) (*T, error)) (*T, error) {
	return lazyLoad(cf, ptr, func() (*T, error) {
		config, err := lockedLazyLoad(&cf.restConfig, cf.LoadRESTConfig)
		if err != nil {
			return nil, err
		}
		return load(config)
	})
}

func lazyLoad[T any](cf *ClientFactory, ptr *atomic.Pointer[T], load func() (*T, error)) (*T, error) {
	if loaded := ptr.Load(); loaded != nil {
		return loaded, nil
	}

	cf.mutex.Lock()
	defer cf.mutex.Unlock()

	return lockedLazyLoad(ptr, load)
}

func lockedLazyLoad[T any](ptr *atomic.Pointer[T], load func() (*T, error)) (*T, error) {
	if loaded := ptr.Load(); loaded != nil {
		return loaded, nil
	}

	loaded, err := load()
	if err == nil {
		ptr.Store(loaded)
	}

	return loaded, err
}

// KubeconfigFromFile returns a [clientcmd.KubeconfigGetter] that tries to load
// a kubeconfig from the given path.
func KubeconfigFromFile(path string) clientcmd.KubeconfigGetter {
	return (&clientcmd.ClientConfigLoadingRules{ExplicitPath: path}).Load
}

// NewClientFromFile creates a new Kubernetes client based of the given
// kubeconfig file.
func NewClientFromFile(kubeconfig string) (kubernetes.Interface, error) {
	return NewClient(KubeconfigFromFile(kubeconfig))
}

func ClientConfig(getter clientcmd.KubeconfigGetter) (*rest.Config, error) {
	kubeconfig, err := getter()
	if err != nil {
		return nil, err
	}

	return clientcmd.NewNonInteractiveClientConfig(*kubeconfig, "", nil, nil).ClientConfig()
}

// NewClient creates new k8s client based of the given kubeconfig getter. This
// should be only used in cases where the client is "short-running" and
// shouldn't/cannot use the common "cached" one.
func NewClient(getter clientcmd.KubeconfigGetter) (kubernetes.Interface, error) {
	config, err := ClientConfig(getter)
	if err != nil {
		return nil, err
	}

	return kubernetes.NewForConfig(config)
}
