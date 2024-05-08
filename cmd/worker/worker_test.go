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

package worker_test

import (
	"io"
	"strconv"
	"strings"
	"testing"

	"github.com/k0sproject/k0s/cmd"
	"github.com/k0sproject/k0s/cmd/worker"
	"github.com/k0sproject/k0s/pkg/constant"

	"github.com/spf13/cobra"

	"github.com/stretchr/testify/assert"
)

func TestWorkerCmd_Args(t *testing.T) {

	newCommand := func(args ...string) *cobra.Command {
		c := worker.NewWorkerCmd()
		c.SetArgs(args)
		c.SetOut(io.Discard)
		c.SetErr(io.Discard)
		return c
	}

	t.Run("none", func(t *testing.T) {
		var ok bool
		underTest := newCommand()
		underTest.RunE = func(cmd *cobra.Command, args []string) error { ok = true; return nil }

		assert.NoError(t, underTest.Execute())
		assert.True(t, ok, "RunE has not been called")
	})

	t.Run("with join token", func(t *testing.T) {
		var ok bool
		underTest := newCommand("the-join-token")
		underTest.RunE = func(cmd *cobra.Command, args []string) error { ok = true; return nil }

		assert.NoError(t, underTest.Execute())
		assert.True(t, ok, "RunE has not been called")
	})

	t.Run("with bogus extra arg", func(t *testing.T) {
		underTest := newCommand("the-join-token", "u-no-pass-more-than-one-arg")
		underTest.RunE = func(cmd *cobra.Command, args []string) error {
			assert.Fail(t, "RunE has been called")
			return nil
		}

		assert.Error(t, underTest.Execute())
	})
}

func TestWorkerCmd_Help(t *testing.T) {
	defaultDataDir := strconv.Quote(constant.DataDirDefault)

	var out strings.Builder
	underTest := cmd.NewRootCmd()
	underTest.SetArgs([]string{"worker", "--help"})
	underTest.SetOut(&out)
	assert.NoError(t, underTest.Execute())

	assert.Equal(t, `Run worker

Usage:
  k0s worker [flags] [[--] <join-token>]

Examples:
	Command to add worker node to the master node:
	CLI argument:
	$ k0s worker <join-token>

	or CLI flag:
	$ k0s worker --token-file <path>
	Note: Token can be passed either as a CLI argument or as a flag

Flags:
      --cidr-range string           HACK: cidr range for the windows worker node (default "10.96.0.0/12")
      --cri-socket string           container runtime socket to use, default to internal containerd. Format: [remote|docker]:[path-to-socket]
      --data-dir string             Data Directory for k0s. DO NOT CHANGE for an existing setup, things will break! (default `+defaultDataDir+`)
  -d, --debug                       Debug logging (default: false)
      --debugListenOn string        Http listenOn for Debug pprof handler (default ":6060")
      --enable-cloud-provider       Whether or not to enable cloud provider support in kubelet
  -h, --help                        help for worker
      --ignore-pre-flight-checks    continue even if pre-flight checks fail
      --iptables-mode string        iptables mode (valid values: nft, legacy, auto). default: auto
      --kubelet-extra-args string   extra args for kubelet
      --labels strings              Node labels, list of key=value pairs
  -l, --logging stringToString      Logging Levels for the different components (default [containerd=info,etcd=info,konnectivity-server=1,kube-apiserver=1,kube-controller-manager=1,kube-scheduler=1,kubelet=1])
      --profile string              worker profile to use on the node (default "default")
      --status-socket string        Full file path to the socket file. (default: <rundir>/status.sock)
      --taints strings              Node taints, list of key=value:effect strings
      --token-file string           Path to the file containing join-token.
  -v, --verbose                     Verbose logging (default: false)
`, out.String())
}
