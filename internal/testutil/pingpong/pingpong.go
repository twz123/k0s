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

package pingpong

import (
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"
)

func Start(t *testing.T, opts ...func(cmd *exec.Cmd)) (*exec.Cmd, *PingPong) {
	pingPong := New(t)
	cmd := exec.Command(pingPong.BinPath(), pingPong.BinArgs()...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	for _, opt := range opts {
		opt(cmd)
	}
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _, _ = cmd.Process.Kill(), cmd.Wait() })
	return cmd, pingPong
}
