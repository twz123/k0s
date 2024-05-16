//go:build unix

/*
Copyright 2023 k0s authors

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
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"
)

type PingPong struct {
	shellPath, pipe string

	mu    sync.Mutex
	state int
}

func New(t *testing.T) *PingPong {
	shellPath, err := exec.LookPath("sh")
	require.NoError(t, err)

	tmpDir := t.TempDir()
	pp := PingPong{
		shellPath: shellPath,
		pipe:      filepath.Join(tmpDir, "pingpong"),
		state:     1,
	}

	err = syscall.Mkfifo(pp.pipe, 0600)
	require.NoError(t, err, "mkfifo failed for %s", pp.pipe)
	return &pp
}

func (pp *PingPong) BinPath() string {
	return pp.shellPath
}

func (pp *PingPong) BinArgs() []string {
	// Only use shell builtins here, we might be running in an empty env.
	return []string{"-euc", `echo ping >"$1" && read pong <"$1"`, "--", pp.pipe}
}

func (pp *PingPong) AwaitPing() (err error) {
	pp.mu.Lock()
	defer pp.mu.Unlock()

	if pp.state != 1 {
		return errors.New("cannot await ping")
	}
	defer func() {
		if err != nil {
			pp.state = -2
		}
	}()
	pp.state = 2

	// The open for reading call will block until the
	// script tries to open the file for writing.
	f, err := os.OpenFile(pp.pipe, os.O_RDONLY, 0)
	if err != nil {
		return err
	}
	_, err = io.Copy(io.Discard, f)
	return errors.Join(err, f.Close())
}

func (pp *PingPong) SendPong() (err error) {
	pp.mu.Lock()
	defer pp.mu.Unlock()

	if pp.state != 2 {
		return errors.New("cannot send pong")
	}
	defer func() {
		if err != nil {
			pp.state = -1
		}
	}()
	pp.state = 1

	// The open for writing call will block until the
	// script tries to open the file for reading.
	f, err := os.OpenFile(pp.pipe, os.O_WRONLY, 0)
	if err != nil {
		return err
	}
	_, err = f.WriteString("pong\n")
	return errors.Join(err, f.Close())
}
