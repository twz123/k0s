//go:build linux

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

package reset

import (
	"errors"
	"fmt"
	"os"

	"github.com/k0sproject/k0s/cmd/internal"
	k0sv1beta1 "github.com/k0sproject/k0s/pkg/apis/k0s/v1beta1"
	"github.com/k0sproject/k0s/pkg/cleanup"
	"github.com/k0sproject/k0s/pkg/component/status"
	"github.com/k0sproject/k0s/pkg/config"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

type command config.CLIOptions

func NewResetCmd() *cobra.Command {
	var (
		debugFlags internal.DebugFlags
		configFlag internal.ConfigFlag
	)

	cmd := &cobra.Command{
		Use:              "reset",
		Short:            "Uninstall k0s. Must be run as root (or with sudo)",
		Args:             cobra.NoArgs,
		PersistentPreRun: debugFlags.Run,
		RunE: func(cmd *cobra.Command, _ []string) error {
			opts, err := config.GetCmdOpts(cmd)
			if err != nil {
				return err
			}
			nodeConfig, err := opts.K0sVars.NodeConfig(configFlag.Loader())
			if err != nil {
				return err
			}

			c := (*command)(opts)
			return c.reset(nodeConfig, debugFlags.IsDebug())
		},
	}

	debugFlags.AddToFlagSet(cmd.PersistentFlags())

	flags := cmd.Flags()
	flags.AddFlagSet(config.GetPersistentFlagSet())
	flags.AddFlagSet(config.GetCriSocketFlag())
	configFlag.WithStdin(cmd.InOrStdin).AddToFlagSet(flags)
	flags.String("kubelet-root-dir", "", "Kubelet root directory for k0s")

	return cmd
}

func (c *command) reset(nodeConfig *k0sv1beta1.ClusterConfig, debug bool) error {
	if os.Geteuid() != 0 {
		return errors.New("this command must be run as root")
	}

	k0sStatus, _ := status.GetStatusInfo(c.K0sVars.StatusSocketPath)
	if k0sStatus != nil && k0sStatus.Pid != 0 {
		return errors.New("k0s seems to be running, please stop k0s before reset")
	}

	if nodeConfig.Spec.Storage.Kine != nil && nodeConfig.Spec.Storage.Kine.DataSource != "" {
		logrus.Warn("Kine dataSource is configured. k0s will not reset the data source if it points to an external database. If you plan to continue using the data source, you should reset it to avoid conflicts.")
	}

	// Get Cleanup Config
	cfg, err := cleanup.NewConfig(debug, c.K0sVars, nodeConfig.Spec.Install.SystemUsers, c.CriSocket)
	if err != nil {
		return fmt.Errorf("failed to configure cleanup: %w", err)
	}

	err = cfg.Cleanup()
	logrus.Info("k0s cleanup operations done.")
	logrus.Warn("To ensure a full reset, a node reboot is recommended.")

	return err
}
