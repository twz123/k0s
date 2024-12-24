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

package install

import (
	"errors"
	"fmt"
	"os"

	"github.com/k0sproject/k0s/cmd/internal"
	"github.com/k0sproject/k0s/pkg/config"
	"github.com/k0sproject/k0s/pkg/install"

	"github.com/spf13/cobra"
)

type command config.CLIOptions

type installFlags struct {
	force   bool
	envVars []string
}

func NewInstallCmd() *cobra.Command {
	var (
		debugFlags   internal.DebugFlags
		installFlags installFlags
	)

	cmd := &cobra.Command{
		Use:              "install",
		Short:            "Install k0s on a brand-new system. Must be run as root (or with sudo)",
		PersistentPreRun: debugFlags.Run,
	}

	pflags := cmd.PersistentFlags()
	debugFlags.AddToFlagSet(pflags)
	pflags.BoolVar(&installFlags.force, "force", false, "force init script creation")
	pflags.StringArrayVarP(&installFlags.envVars, "env", "e", nil, "set environment variable")
	pflags.AddFlagSet(config.GetPersistentFlagSet())

	cmd.AddCommand(installControllerCmd(&installFlags))
	cmd.AddCommand(installWorkerCmd(&installFlags))

	return cmd
}

// The setup functions:
//   - Ensures that the proper users are created.
//   - Sets up startup and logging for k0s.
func (c *command) setup(role string, args []string, installFlags *installFlags) error {
	if os.Geteuid() != 0 {
		return errors.New("this command must be run as root")
	}

	nodeConfig, err := c.K0sVars.NodeConfig()
	if err != nil {
		return err
	}

	if role == "controller" {
		systemUsers := nodeConfig.Spec.Install.SystemUsers
		homeDir := c.K0sVars.DataDir
		if err := install.EnsureControllerUsers(systemUsers, homeDir); err != nil {
			return fmt.Errorf("failed to create controller users: %w", err)
		}
	}
	err = install.EnsureService(args, installFlags.envVars, installFlags.force)
	if err != nil {
		return fmt.Errorf("failed to install k0s service: %w", err)
	}
	return nil
}
