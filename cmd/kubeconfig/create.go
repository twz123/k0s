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

package kubeconfig

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"html/template"
	"os"
	"path"

	"github.com/k0sproject/k0s/pkg/certificate"
	"github.com/k0sproject/k0s/pkg/config"
	"github.com/sirupsen/logrus"

	"github.com/spf13/cobra"
)

var userKubeconfigTemplate = template.Must(template.New("kubeconfig").Parse(`
apiVersion: v1
clusters:
- cluster:
    server: {{.JoinURL}}
    certificate-authority-data: {{.CACert}}
  name: k0s
contexts:
- context:
    cluster: k0s
    user: {{.User}}
  name: k0s
current-context: k0s
kind: Config
preferences: {}
users:
- name: {{.User}}
  user:
    client-certificate-data: {{.ClientCert}}
    client-key-data: {{.ClientKey}}
`))

func kubeconfigCreateCmd() *cobra.Command {
	var groups string

	cmd := &cobra.Command{
		Use:   "create username",
		Short: "Create a kubeconfig for a user",
		Long: `Create a kubeconfig with a signed certificate and public key for a given user (and optionally user groups)
Note: A certificate once signed cannot be revoked for a particular user`,
		Example: `	Command to create a kubeconfig for a user:
	CLI argument:
	$ k0s kubeconfig create username

	optionally add groups:
	$ k0s kubeconfig create username --groups [groups]`,
		PreRun: func(cmd *cobra.Command, args []string) {
			// ensure logs don't mess up the output
			logrus.SetOutput(cmd.ErrOrStderr())
		},
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			username := args[0]
			if username == "" {
				return errors.New("username cannot be empty")
			}

			opts, err := config.GetCmdOpts(cmd)
			if err != nil {
				return err
			}
			nodeConfig, err := opts.K0sVars.NodeConfig()
			if err != nil {
				return err
			}
			clusterAPIURL := nodeConfig.Spec.API.APIAddressURL()

			caCert, err := os.ReadFile(path.Join(opts.K0sVars.CertRootDir, "ca.crt"))
			if err != nil {
				return fmt.Errorf("failed to read cluster ca certificate: %w, check if the control plane is initialized on this node", err)
			}
			caCertPath, caCertKey := path.Join(opts.K0sVars.CertRootDir, "ca.crt"), path.Join(opts.K0sVars.CertRootDir, "ca.key")
			userReq := certificate.Request{
				Name:   username,
				CN:     username,
				O:      groups,
				CACert: caCertPath,
				CAKey:  caCertKey,
			}
			certManager := certificate.Manager{
				K0sVars: opts.K0sVars,
			}
			userCert, err := certManager.EnsureCertificate(userReq, "root")
			if err != nil {
				return err
			}

			data := struct {
				CACert     string
				ClientCert string
				ClientKey  string
				User       string
				JoinURL    string
			}{
				CACert:     base64.StdEncoding.EncodeToString(caCert),
				ClientCert: base64.StdEncoding.EncodeToString([]byte(userCert.Cert)),
				ClientKey:  base64.StdEncoding.EncodeToString([]byte(userCert.Key)),
				User:       username,
				JoinURL:    clusterAPIURL,
			}

			var buf bytes.Buffer

			err = userKubeconfigTemplate.Execute(&buf, &data)
			if err != nil {
				return err
			}
			_, err = cmd.OutOrStdout().Write(buf.Bytes())
			if err != nil {
				return err
			}
			return nil
		},
	}

	cmd.Flags().AddFlagSet(config.FileInputFlag())
	cmd.Flags().StringVar(&groups, "groups", "", "Specify groups")
	cmd.PersistentFlags().AddFlagSet(config.GetPersistentFlagSet())
	return cmd
}
