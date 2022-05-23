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
package clusterconfig

import (
	"context"
	"fmt"
	"time"

	k0sclient "github.com/k0sproject/k0s/pkg/apis/k0s.k0sproject.io/clientset/typed/k0s.k0sproject.io/v1beta1"
	"github.com/k0sproject/k0s/pkg/apis/k0s.k0sproject.io/v1beta1"
	"github.com/k0sproject/k0s/pkg/component"
	"github.com/k0sproject/k0s/pkg/config"
	"github.com/k0sproject/k0s/pkg/constant"
	"github.com/k0sproject/k0s/static"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/sirupsen/logrus"
)

type bootstrapper struct {
	log logrus.FieldLogger

	k0sVars        constant.CfgVars
	configClient   k0sclient.ClusterConfigInterface
	isLeader       func() bool
	manifestsSaver func(name string, manifest []byte) error
}

// NewBootstrapper returns a component that unpacks k0s's CRD manifests and
// ensures that the global cluster config object exists, creating it from the
// local configuration if it doesn't exist already.
func NewBootstrapper(
	k0sVars constant.CfgVars,
	configClient k0sclient.ClusterConfigInterface,
	isLeader func() bool,
	manifestsSaver func(name string, manifest []byte) error,
) component.Component {
	return &bootstrapper{
		log: logrus.WithFields(logrus.Fields{"component": "clusterconfig_bootstrapper"}),

		k0sVars:        k0sVars,
		configClient:   configClient,
		isLeader:       isLeader,
		manifestsSaver: manifestsSaver,
	}
}

func (b *bootstrapper) Init(context.Context) error {
	return b.unpackCRDs()
}

func (b *bootstrapper) Run(ctx context.Context) error {
	err := b.bootstrapConfigObject(ctx)
	if err != nil {
		return fmt.Errorf("failed to bootstrap cluster config object: %v", err)
	}

	return err
}

func (*bootstrapper) Healthy() error { return nil }
func (*bootstrapper) Stop() error    { return nil }

func (b *bootstrapper) unpackCRDs() error {
	const manifestDir = "manifests/v1beta1/CustomResourceDefinition"

	manifestNames, err := static.AssetDir(manifestDir)
	if err != nil {
		return fmt.Errorf("failed to list CRD manifests: %w", err)
	}

	for _, manifestName := range manifestNames {
		content, err := static.Asset(fmt.Sprintf("%s/%s", manifestDir, manifestName))
		if err != nil {
			return fmt.Errorf("failed to load CRD manifest %q: %w", manifestName, err)
		}
		err = b.manifestsSaver(manifestName, content)
		if err != nil {
			return fmt.Errorf("failed to save CRD manifest %q: %w", manifestName, err)
		}
	}

	return nil
}

func (b *bootstrapper) bootstrapConfigObject(ctx context.Context) error {
	var localConfig *v1beta1.ClusterConfig

	// We need to wait until we either verified the existence of the cluster config object, or we succeed in creating it.
	return wait.PollWithContext(ctx, 1*time.Second, 20*time.Second, func(ctx context.Context) (bool, error) {
		if exists, err := configObjectExists(ctx, b.configClient); err != nil {
			b.log.WithError(err).Error("Failed to verify existence of the cluster config object")
			return false, nil
		} else if exists {
			b.log.Info("Verified existence of the cluster config object")
			return true, nil
		}

		if localConfig == nil {
			loadingRules := config.ClientConfigLoadingRules{K0sVars: b.k0sVars}
			parsedConfig, err := loadingRules.ParseRuntimeConfig()
			if err != nil {
				return false, fmt.Errorf("failed to parse local cluster config: %w", err)
			}
			localConfig = parsedConfig.GetClusterWideConfig().StripDefaults().CRValidator()
		}

		if !b.isLeader() {
			b.log.Info("I am not the leader, not creating cluster config object")
			return true, nil
		}

		err := createConfigObject(ctx, b.configClient, localConfig)
		if err != nil {
			b.log.WithError(err).Error("Failed to create cluster config object")
			return false, nil
		}

		b.log.Info("Successfully created cluster config object")
		return true, nil
	})
}

func configObjectExists(ctx context.Context, configClient k0sclient.ClusterConfigInterface) (bool, error) {
	timeout, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	_, err := configClient.Get(timeout, constant.ClusterConfigObjectName, metav1.GetOptions{})
	if err == nil {
		return true, nil
	}
	if errors.IsNotFound(err) {
		return false, nil
	}

	return false, err
}

func createConfigObject(ctx context.Context, client k0sclient.ClusterConfigInterface, cc *v1beta1.ClusterConfig) error {
	timeout, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	_, err := client.Create(timeout, cc, metav1.CreateOptions{})
	return err
}
