/*
Copyright 2024 k0s authors

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

package internal

import (
	"github.com/spf13/cobra"
)

// CallParentPersistentPreRun runs the parent command's persistent pre-run.
// Cobra does not do this automatically.
//
// See: https://github.com/spf13/cobra/issues/216
// See: https://github.com/spf13/cobra/blob/v1.4.0/command.go#L833-L843
func CallParentPersistentPreRun(cmd *cobra.Command, args []string) error {
	for p := cmd.Parent(); p != nil; p = p.Parent() {
		preRunE := p.PersistentPreRunE
		preRun := p.PersistentPreRun

		p.PersistentPreRunE = nil
		p.PersistentPreRun = nil

		defer func() {
			p.PersistentPreRunE = preRunE
			p.PersistentPreRun = preRun
		}()

		if preRunE != nil {
			return preRunE(cmd, args)
		}

		if preRun != nil {
			preRun(cmd, args)
			return nil
		}
	}

	return nil
}
