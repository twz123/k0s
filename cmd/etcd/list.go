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
	"encoding/json"
	"errors"
	"fmt"

	k0sv1beta1 "github.com/k0sproject/k0s/pkg/apis/k0s/v1beta1"
	"github.com/k0sproject/k0s/pkg/config"
	"github.com/k0sproject/k0s/pkg/etcd"

	"github.com/spf13/cobra"
)

func etcdListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "member-list",
		Short: "List etcd cluster members (JSON encoded)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			opts, err := config.GetCmdOpts(cmd)
			if err != nil {
				return err
			}
			nodeConfig, err := opts.K0sVars.NodeConfig()
			if err != nil {
				return err
			}
			if nodeConfig.Spec.Storage.Type != k0sv1beta1.EtcdStorageType {
				return errors.New("wrong storage type: " + string(nodeConfig.Spec.Storage.Type))
			}

			ctx := cmd.Context()
			etcdClient, err := etcd.NewClient(opts.K0sVars.CertRootDir, opts.K0sVars.EtcdCertDir, nodeConfig.Spec.Storage.Etcd)
			if err != nil {
				return fmt.Errorf("can't list etcd cluster members: %w", err)
			}
			members, err := etcdClient.ListMembers(ctx)
			if err != nil {
				return fmt.Errorf("can't list etcd cluster members: %w", err)
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]interface{}{"members": members})
		},
	}

	cmd.Flags().AddFlagSet(config.GetPersistentFlagSet())

	return cmd
}
