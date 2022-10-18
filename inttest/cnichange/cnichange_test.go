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

package cnichange

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/k0sproject/k0s/internal/pkg/buf"
	"github.com/k0sproject/k0s/internal/pkg/buf/ring"
	"github.com/k0sproject/k0s/inttest/common"

	"github.com/stretchr/testify/suite"
)

type CNIChangeSuite struct {
	common.FootlooseSuite
}

func (s *CNIChangeSuite) TestK0sGetsUpButRejectsToChangeCNI() {
	// Run controller with defaults only --> kube-router in use
	s.NoError(s.InitController(0))

	// Restart the controller with new config, should fail as the CNI change is not supported
	sshC1, err := s.SSH(s.ControllerNode(0))
	s.Require().NoError(err)
	defer sshC1.Disconnect()
	s.T().Log("killing k0s")
	_, err = sshC1.ExecWithOutput(s.Context(), "kill $(pidof k0s) && while pidof k0s; do sleep 0.1s; done")
	s.Require().NoError(err)

	s.PutFile(s.ControllerNode(0), "/tmp/k0s.yaml", k0sConfig)
	s.T().Log("restarting k0s with new cni, this should fail")

	lastLogLines := ring.NewBuffer[string](10)
	push := func() func(prefix string, line []byte) error {
		var mu sync.Mutex
		return func(prefix string, line []byte) error {
			mu.Lock()
			defer mu.Unlock()
			lastLogLines.PushBack(prefix + string(line))
			return nil
		}
	}()

	outWriter := buf.LineWriter{WriteLine: func(line []byte) error { return push("k0s-out: ", line) }}
	errWriter := buf.LineWriter{WriteLine: func(line []byte) error { return push("k0s-err: ", line) }}

	// If this doesn't fail within a minute, the startup probably succeeded.
	ctx, cancel := context.WithCancel(s.Context())
	defer cancel()
	var timeout atomic.Bool
	go func() {
		select {
		case <-ctx.Done():
		case <-time.After(1 * time.Minute):
			timeout.Store(true)
			cancel()
		}
	}()

	err = sshC1.Exec(ctx, "/usr/local/bin/k0s controller --config /tmp/k0s.yaml", common.SSHStreams{
		Out: &outWriter, Err: &errWriter,
	})
	_, _ = outWriter.Flush(false), errWriter.Flush(false)
	lastLogLines.ForEach(func(line string) { s.T().Log(line) })
	s.Require().False(timeout.Load(), "Timed out while waiting for k0s to fail")
	s.Require().Error(err)
}

func TestCNIChangeSuite(t *testing.T) {
	s := CNIChangeSuite{
		common.FootlooseSuite{
			ControllerCount: 1,
			WorkerCount:     0,
		},
	}
	suite.Run(t, &s)
}

const k0sConfig = `
spec:
  network:
    provider: calico
    calico:
`
