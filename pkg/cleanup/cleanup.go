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

package cleanup

import (
	"errors"
	"fmt"

	"github.com/k0sproject/k0s/pkg/component/worker"
	"github.com/k0sproject/k0s/pkg/config"

	"github.com/k0sproject/k0s/pkg/container/runtime"
	"github.com/sirupsen/logrus"
)

type Config struct {
	debug                    bool
	externalContainerRuntime bool
	containerRuntime         runtime.ContainerRuntime
	k0sVars                  *config.CfgVars
}

func NewConfig(debug bool, k0sVars *config.CfgVars, criSocketFlag string) (*Config, error) {
	runtimeEndpoint, err := worker.GetContainerRuntimeEndpoint(criSocketFlag, k0sVars.RunDir)
	if err != nil {
		return nil, err
	}

	return &Config{
		debug:                    debug,
		externalContainerRuntime: criSocketFlag == "",
		containerRuntime:         runtime.NewContainerRuntime(runtimeEndpoint),
		k0sVars:                  k0sVars,
	}, nil
}

func (c *Config) Cleanup() error {
	var errs []error
	cleanupSteps := []Step{
		&containers{
			c.debug,
			c.externalContainerRuntime,
			c.containerRuntime,
			c.k0sVars,
		},
		&users{Config: c},
		&services{Config: c},
		&directories{Config: c},
		&cni{},
	}

	if bridge := newBridgeStep(); bridge != nil {
		cleanupSteps = append(cleanupSteps, bridge)
	}

	for _, step := range cleanupSteps {
		logrus.Info("* ", step.Name())
		err := step.Run()
		if err != nil {
			logrus.Debug(err)
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("errors occurred during clean-up: %w", errors.Join(errs...))
	}
	return nil
}

// Step interface is used to implement cleanup steps
type Step interface {
	// Run impelements specific cleanup operations
	Run() error
	// Name returns name of the step for conveninece
	Name() string
}
