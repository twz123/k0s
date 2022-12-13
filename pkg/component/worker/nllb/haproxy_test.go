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

package nllb

import (
	"bufio"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	k0snet "github.com/k0sproject/k0s/internal/pkg/net"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteHAProxyConfigFiles(t *testing.T) {
	for _, test := range []struct {
		name     string
		expected uint
		servers  []string
	}{
		{"empty", 0, []string{}},
		{"one", 1, []string{"foo:16"}},
		{"two", 2, []string{"foo:16", "bar:17"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			configDir := t.TempDir()
			reloadDir := t.TempDir()
			params := haproxyParams{
				configDir:         configDir,
				reloadDir:         reloadDir,
				bindIP:            net.IPv6loopback,
				apiServerBindPort: 6443,
			}
			filesParams := haproxyFilesParams{
				konnectivityServerBindPort: 7132,
				konnectivityServerPort:     8132,
			}
			for _, server := range test.servers {
				server, err := k0snet.ParseHostPort(server)
				require.NoError(t, err)
				filesParams.apiServers = append(filesParams.apiServers, *server)
			}

			require.NoError(t, writeHAProxyConfigFiles(&params, &filesParams))

			t.Run("haproxy.cfg", func(t *testing.T) {
				f, err := os.Open(filepath.Join(configDir, "haproxy.cfg"))
				require.NoError(t, err)
				defer f.Close()
				scanner := bufio.NewScanner(bufio.NewReader(f))
				scanner.Split(bufio.ScanLines)
				var lines []string
				for scanner.Scan() {
					lines = append(lines, scanner.Text())
				}

				var numAPIServers uint
				for _, line := range lines {
					if strings.HasPrefix(line, "  server apiserver-") && strings.HasSuffix(line, " check") {
						numAPIServers++
					}
				}

				assert.Equal(t, test.expected, numAPIServers)
			})

			t.Run("reload", func(t *testing.T) {
				stat, err := os.Stat(filepath.Join(reloadDir, "reload"))
				require.NoError(t, err)
				assert.False(t, stat.IsDir())
				assert.Equal(t, fs.FileMode(0666), stat.Mode())
				assert.Equal(t, int64(0), stat.Size())
			})
		})
	}
}
