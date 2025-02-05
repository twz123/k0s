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

package config

import (
	"errors"
	"fmt"

	"github.com/k0sproject/k0s/cmd/internal"
	"github.com/k0sproject/k0s/pkg/apis/k0s/v1beta1"
	"github.com/k0sproject/k0s/pkg/config"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func NewValidateCmd() *cobra.Command {
	var configFlag internal.ConfigFlag

	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate k0s configuration",
		Long: `Example:
   k0s config validate --config path_to_config.yaml`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			bytes, err := configFlag.Loader()()
			if err != nil {
				return fmt.Errorf("failed to read configuration: %w", err)
			}

			cfg, err := v1beta1.ConfigFromBytes(bytes)
			if err != nil {
				return fmt.Errorf("failed to parse configuration: %w", err)
			}

			return errors.Join(cfg.Validate()...)
		},
	}

	flags := cmd.Flags()
	config.GetPersistentFlagSet().VisitAll(func(f *pflag.Flag) {
		f.Hidden = true
		f.Deprecated = "it has no effect and will be removed in a future release"
		cmd.PersistentFlags().AddFlag(f)
	})

	configFlag.WithStdin(cmd.InOrStdin).Required().AddToFlagSet(flags)

	return cmd
}
