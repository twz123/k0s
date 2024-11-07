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

package reset

import (
	"bytes"
	_ "embed"
	"fmt"
	"io"
	"strings"
	"testing"

	testifysuite "github.com/stretchr/testify/suite"

	"github.com/k0sproject/k0s/inttest/common"
)

type suite struct {
	common.BootlooseSuite
}

//go:embed clutter-data-dir.sh
var clutterScript []byte

func (s *suite) TestReset() {
	ctx := s.Context()
	workerNode := s.WorkerNode(0)

	if !s.Run("k0s gets up", func() {
		s.Require().NoError(s.InitController(0, "--disable-components=konnectivity-server,metrics-server"))
		s.Require().NoError(s.RunWorkers())

		kc, err := s.KubeClient(s.ControllerNode(0))
		s.Require().NoError(err)

		err = s.WaitForNodeReady(workerNode, kc)
		s.NoError(err)

		s.T().Log("waiting to see CNI pods ready")
		s.NoError(common.WaitForKubeRouterReady(ctx, kc), "CNI did not start")

		ssh, err := s.SSH(ctx, workerNode)
		s.Require().NoError(err)
		defer ssh.Disconnect()

		s.NoError(ssh.Exec(ctx, "test -d /var/lib/k0s", common.SSHStreams{}), "/var/lib/k0s is not a directory")
		s.NoError(ssh.Exec(ctx, "test -d /run/k0s", common.SSHStreams{}), "/run/k0s is not a directory")

		s.NoError(ssh.Exec(ctx, "pidof containerd-shim-runc-v2 >&2", common.SSHStreams{}), "Expected some running containerd shims")
	}) {
		return
	}

	var clutteringPaths bytes.Buffer

	if !s.Run("prepare k0s reset", func() {
		s.NoError(s.StopWorker(workerNode), "Failed to stop k0s")

		ssh, err := s.SSH(ctx, workerNode)
		s.Require().NoError(err)
		defer ssh.Disconnect()

		streams, flushStreams := common.TestLogStreams(s.T(), "clutter data dir")
		streams.In = bytes.NewReader(clutterScript)
		streams.Out = io.MultiWriter(&clutteringPaths, streams.Out)
		err = ssh.Exec(ctx, "sh -s -- /var/lib/k0s", streams)
		flushStreams()
		s.Require().NoError(err)
	}) {
		return
	}

	s.Run("k0s reset", func() {
		ssh, err := s.SSH(ctx, workerNode)
		s.Require().NoError(err)
		defer ssh.Disconnect()

		streams, flushStreams := common.TestLogStreams(s.T(), "reset")
		err = ssh.Exec(ctx, "k0s reset --debug", streams)
		flushStreams()
		s.NoError(err, "k0s reset didn't exit cleanly")

		for _, path := range strings.Split(string(bytes.TrimSpace(clutteringPaths.Bytes())), "\n") {
			if !strings.HasPrefix(path, "/var/lib/k0s/") {
				s.NoError(ssh.Exec(ctx, fmt.Sprintf("test -e %q", path), common.SSHStreams{}), "Failed to verify existence of %s", path)
			}
		}

		// Check that only the bind mounts are still there.
		var remainingPaths strings.Builder
		if s.NoError(ssh.Exec(ctx, "find /var/lib/k0s -mindepth 1 -maxdepth 3 -print0", common.SSHStreams{Out: &remainingPaths}), "failed to list contents of /var/lib/k0s") {
			remainingPaths := strings.Split(strings.Trim(remainingPaths.String(), "\x00"), "\x00")
			s.ElementsMatch(remainingPaths, []string{
				"/var/lib/k0s/cluttered",
				"/var/lib/k0s/cluttered/bind_dir",
				"/var/lib/k0s/cluttered/bind_dir/in_overmount_dir.txt",
				"/var/lib/k0s/cluttered/bind_file.txt",
				"/var/lib/k0s/cluttered/rbind_dir",
				"/var/lib/k0s/cluttered/rbind_dir/real_recursive_dir.txt",
				"/var/lib/k0s/cluttered/rbind_dir/bind_dir",
				"/var/lib/k0s/cluttered/rbind_dir/bind_file.txt",
			})
		}

		s.NoError(ssh.Exec(ctx, "! test -e /run/k0s", common.SSHStreams{}), "/run/k0s still exists")
		s.NoError(ssh.Exec(ctx, "! pidof containerd-shim-runc-v2 >&2", common.SSHStreams{}), "Expected no running containerd shims")
	})
}

func TestResetSuite(t *testing.T) {
	testifysuite.Run(t, &suite{
		common.BootlooseSuite{
			ControllerCount: 1,
			WorkerCount:     1,
		},
	})
}
