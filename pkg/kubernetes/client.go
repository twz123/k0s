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

// NewAdminClientFactory creates a new factory that loads the admin kubeconfig based client
func NewAdminClientFactory(kubeconfigPath string) ClientFactoryInterface {
	return &clientFactory{
		configPath: kubeconfigPath,
	}
}

// clientFactory implements a cached and lazy-loading clientFactory for all the different types of kube clients we use
// It's implemented as lazy-loading so we can create the factory itself before we have the api, etcd and other components up so we can pass
// the factory itself to components needing kube clients and creation time.
type clientFactory struct {
	configPath string

	client              slot[kubernetes.Clientset]
	dynamicClient       slot[dynamic.DynamicClient]
	discoveryClient     slot[discovery.CachedDiscoveryInterface]
	apiExtensionsClient slot[apiextensionsclientset.Clientset]
	k0sClient           slot[k0sclientset.Clientset]
	restConfig          slot[rest.Config]
}

func (c *clientFactory) GetClient() (kubernetes.Interface, error) {
	return getClient(c, &c.client, kubernetes.NewForConfig)
}

func (c *clientFactory) GetDynamicClient() (dynamic.Interface, error) {
	return getClient(c, &c.dynamicClient, dynamic.NewForConfig)
}

func (c *clientFactory) GetDiscoveryClient() (discovery.CachedDiscoveryInterface, error) {
	client, err := getClient(c, &c.discoveryClient, func(c *rest.Config) (*discovery.CachedDiscoveryInterface, error) {
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

func (c *clientFactory) GetAPIExtensionsClient() (apiextensionsclientset.Interface, error) {
	return getClient(c, &c.apiExtensionsClient, apiextensionsclientset.NewForConfig)
}

func (c *clientFactory) GetK0sClient() (k0sclientset.Interface, error) {
	return getClient(c, &c.k0sClient, k0sclientset.NewForConfig)
}

// Deprecated: Use [clientFactory.GetK0sClient] instead.
func (c *clientFactory) GetConfigClient() (cfgClient.ClusterConfigInterface, error) {
	k0sClient, err := c.GetK0sClient()
	if err != nil {
		return nil, err
	}

	return k0sClient.K0sV1beta1().ClusterConfigs(constant.ClusterConfigNamespace), nil
}

// Deprecated: Use [clientFactory.GetK0sClient] instead.
func (c *clientFactory) GetEtcdMemberClient() (etcdMemberClient.EtcdMemberInterface, error) {
	k0sClient, err := c.GetK0sClient()
	if err != nil {
		return nil, err
	}

	return k0sClient.EtcdV1beta1().EtcdMembers(), nil
}

func (c *clientFactory) GetRESTConfig() (*rest.Config, error) {
	config, err := c.getRESTConfig()
	if err != nil {
		return nil, err
	}

	return rest.CopyConfig(config), nil
}

func (c *clientFactory) getRESTConfig() (*rest.Config, error) {
	return c.restConfig.get(func() (*rest.Config, error) {
		config, err := ClientConfig(KubeconfigFromFile(c.configPath))
		if err != nil {
			return nil, err
		}

		// We're always running the client on the same host as the API, no need to compress
		config.DisableCompression = true
		// To mitigate stack applier bursts in startup
		config.QPS = 40.0
		config.Burst = 400.0

		return config, nil
	})
}

func getClient[T any](cf *clientFactory, client *slot[T], load func(*rest.Config) (*T, error)) (*T, error) {
	return client.get(func() (*T, error) {
		config, err := cf.getRESTConfig()
		if err != nil {
			return nil, err
		}
		return load(config)
	})
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

// Caches pointers to T that are loaded via some load func if that func doesn't
// return an error.
type slot[T any] struct {
	atomic.Pointer[T]
	sync.Mutex
}

func (s *slot[T]) get(load func() (*T, error)) (*T, error) {
	if loaded := s.Load(); loaded != nil {
		return loaded, nil
	}

	s.Lock()
	defer s.Unlock()

	if loaded := s.Load(); loaded != nil {
		return loaded, nil
	}

	loaded, err := load()
	if err == nil {
		s.Store(loaded)
	}

	return loaded, err
}
