// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package internal

import (
	"github.com/k0sproject/k0s/pkg/component/status"
	"github.com/k0sproject/k0s/pkg/config"

	"github.com/spf13/cobra"
)

// CmdOpts are the CLI options of a command, plus the status of the k0s
// instance running on this node, if any.
type CmdOpts struct {
	*config.CLIOptions
	Status *status.K0sStatus
}

// GetCmdOpts returns the CLI options of cmd. If a k0s instance is running on
// this node, its variables take precedence over the ones derived from the
// command line, so that commands operate on the running instance without
// having to repeat its flags, e.g. --data-dir.
func GetCmdOpts(cmd *cobra.Command) (*CmdOpts, error) {
	opts, err := config.GetCmdOpts(cmd)
	if err != nil {
		return nil, err
	}

	// Any error means that k0s isn't running on this node.
	k0sStatus, err := status.GetStatusInfoWithoutProbe(opts.K0sVars.StatusSocketPath)
	if err != nil {
		return &CmdOpts{CLIOptions: opts}, nil
	}

	if k0sStatus.K0sVars != nil {
		opts.K0sVars = k0sStatus.K0sVars
	}

	return &CmdOpts{opts, k0sStatus}, nil
}
