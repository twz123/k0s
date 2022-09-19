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

package etcd

import (
	"context"
	"fmt"
	"net"
	"net/url"

	"github.com/k0sproject/k0s/pkg/config"
	"github.com/k0sproject/k0s/pkg/etcd"
	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

func etcdLeaveCmd() *cobra.Command {
	var peerAddress string
	var peerURL string

	cmd := &cobra.Command{
		Use:   "leave",
		Short: "Sign off a given etc node from etcd cluster",
		RunE: func(cmd *cobra.Command, args []string) error {
			c := config.GetCmdOpts()
			spec, err := c.LoadControlPlaneSpec()
			if err != nil {
				return err
			}

			if spec.Storage.Type != config.EtcdStorageType {
				return fmt.Errorf("wrong storage type %q", spec.Storage.Type)
			}

			if peerURL == "" {
				if peerAddress != "" {
					peerURL = (&url.URL{Scheme: "https", Host: net.JoinHostPort(peerAddress, "2380")}).String()
					logrus.Warnf("The flag --peer-address=%q is deprecated in favor of --peer-url=%q", peerAddress, peerURL)
				} else {
					peerURL = spec.Storage.Etcd.PeerURL().String()
				}
			}
			if peerURL == "" {
				return fmt.Errorf("can't leave etcd cluster: no peer URL, check the config file or use CLI argument")
			}

			ctx := context.Background()

			etcdClient, err := etcd.ConfigFromSpec(&c.K0sVars, &spec.Storage).NewClient()
			if err != nil {
				return err
			}

			log := logrus.WithField("peerURL", peerURL)
			peerID, err := etcdClient.GetPeerIDByAddress(ctx, peerURL)
			if err != nil {
				log.WithError(err).Error("Failed to get peer ID")
				return err
			}

			log = log.WithField("peerID", peerID)

			if err := etcdClient.DeleteMember(ctx, peerID); err != nil {
				log.WithError(err).Error("Failed to delete member")
				return err
			}

			log.Info("Member successfully deleted")
			return nil
		},
	}

	cmd.Flags().StringVar(&peerURL, "peer-url", "", `etcd peer URL (e.g. "https://127.0.0.1:2380")`)
	cmd.Flags().StringVar(&peerAddress, "peer-address", "", "etcd peer address (deprecated; use --peer-url instead)")
	cmd.PersistentFlags().AddFlagSet(config.GetPersistentFlagSet())
	return cmd
}
