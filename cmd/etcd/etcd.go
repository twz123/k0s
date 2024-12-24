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

package etcd

import (
	"errors"
	"fmt"

	"github.com/k0sproject/k0s/cmd/internal"
	"github.com/k0sproject/k0s/pkg/apis/k0s/v1beta1"
	"github.com/k0sproject/k0s/pkg/config"

	"github.com/spf13/cobra"
)

func NewEtcdCmd() *cobra.Command {
	var debugFlags internal.DebugFlags

	cmd := &cobra.Command{
		Use:   "etcd",
		Short: "Manage etcd cluster",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			debugFlags.Run(cmd, args)

			opts, err := config.GetCmdOpts(cmd)
			if err != nil {
				return err
			}
			nodeConfig, err := opts.K0sVars.NodeConfig()
			if err != nil {
				return err
			}
			if nodeConfig.Spec.Storage.Type != v1beta1.EtcdStorageType {
				return fmt.Errorf("wrong storage type: %s", nodeConfig.Spec.Storage.Type)
			}
			if nodeConfig.Spec.Storage.Etcd.IsExternalClusterUsed() {
				return errors.New("command 'k0s etcd' does not support external etcd cluster")
			}
			return nil
		},
	}

	pflags := cmd.PersistentFlags()
	debugFlags.AddToFlagSet(pflags)
	pflags.AddFlagSet(config.GetPersistentFlagSet())

	cmd.AddCommand(etcdLeaveCmd())
	cmd.AddCommand(etcdListCmd())

	return cmd
}
