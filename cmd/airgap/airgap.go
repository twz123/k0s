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

package airgap

import (
	"github.com/spf13/cobra"

	"github.com/k0sproject/k0s/cmd/internal"
	"github.com/k0sproject/k0s/pkg/config"
)

func NewAirgapCmd() *cobra.Command {
	var debugFlags internal.DebugFlags

	cmd := &cobra.Command{
		Use:              "airgap",
		Short:            "Manage airgap setup",
		PersistentPreRun: debugFlags.Run,
	}

	pflags := cmd.PersistentFlags()
	debugFlags.AddToFlagSet(pflags)
	pflags.AddFlagSet(config.FileInputFlag())
	pflags.AddFlagSet(config.GetPersistentFlagSet())

	cmd.AddCommand(NewAirgapListImagesCmd())

	return cmd
}
