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
	"context"
	"io"
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
			underTest := haproxy{
				configDir: configDir,
				reloadDir: reloadDir,
				config: &haproxyConfig{
					haproxyParams: haproxyParams{
						bindIP:            net.IPv6loopback,
						apiServerBindPort: 6443,
					},
					haproxyFilesParams: haproxyFilesParams{
						konnectivityServerBindPort: 7132,
						konnectivityServerPort:     8132,
					},
				},
			}
			for _, server := range test.servers {
				server, err := k0snet.ParseHostPort(server)
				require.NoError(t, err)
				underTest.config.apiServers = append(underTest.config.apiServers, *server)
			}

			require.NoError(t, underTest.writeConfigFiles())

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

func Test_ShowServersState_Success(t *testing.T) {
	output := strings.Join([]string{
		"1",
		"# be_id be_name srv_id srv_name srv_addr srv_op_state srv_admin_state srv_uweight srv_iweight srv_time_since_last_change srv_check_status srv_check_result srv_check_health srv_check_state srv_agent_state bk_f_forced_id srv_f_forced_id srv_fqdn srv_port srvrecord srv_use_ssl srv_check_port srv_check_addr srv_agent_addr srv_agent_port",
		"3 upstream_apiservers 1 apiserver-0 10.81.146.113 2 0 1 1 10787 6 3 4 6 0 0 0 - 6443 - 0 0 - - 0",
		"3 upstream_apiservers 2 apiserver-1 10.81.146.184 2 0 1 1 10787 6 3 4 6 0 0 0 - 6443 - 0 0 - - 0",
		"3 upstream_apiservers 3 apiserver-2 10.81.146.254 2 0 1 1 10787 6 3 4 6 0 0 0 - 6443 - 0 0 - - 0",
		"5 upstream_konnectivity_servers 1 konnectivity-server-0 10.81.146.113 2 0 1 1 10787 1 0 2 0 0 0 0 - 8132 - 0 0 - - 0",
		"5 upstream_konnectivity_servers 2 konnectivity-server-1 10.81.146.184 2 0 1 1 10787 1 0 2 0 0 0 0 - 8132 - 0 0 - - 0",
		"5 upstream_konnectivity_servers 3 konnectivity-server-2 10.81.146.254 2 0 1 1 10787 1 0 2 0 0 0 0 - 8132 - 0 0 - - 0",
		"",
		"",
	}, "\n")

	sendRaw := func(_ context.Context, cmd []byte) (io.ReadCloser, error) {
		require.Equal(t, []byte("show servers state\n"), cmd)
		return io.NopCloser(strings.NewReader(output)), nil
	}

	underTest := haproxySender{sendRaw}

	servers, err := underTest.ShowServersState(context.TODO())
	require.NoError(t, err)
	assert.Len(t, servers, 2)

	backendServers := servers["upstream_apiservers"]
	if assert.Len(t, backendServers, 3) {
		assert.Equal(t, backendServers[0].name, "apiserver-0")
		assert.Equal(t, backendServers[0].address.Host(), "10.81.146.113")
		assert.Equal(t, backendServers[0].address.Port(), uint16(6443))
		assert.Equal(t, backendServers[1].name, "apiserver-1")
		assert.Equal(t, backendServers[1].address.Host(), "10.81.146.184")
		assert.Equal(t, backendServers[1].address.Port(), uint16(6443))
		assert.Equal(t, backendServers[2].name, "apiserver-2")
		assert.Equal(t, backendServers[2].address.Host(), "10.81.146.254")
		assert.Equal(t, backendServers[2].address.Port(), uint16(6443))
	}

	backendServers = servers["upstream_konnectivity_servers"]
	if assert.Len(t, backendServers, 3) {
		assert.Equal(t, backendServers[0].name, "konnectivity-server-0")
		assert.Equal(t, backendServers[0].address.Host(), "10.81.146.113")
		assert.Equal(t, backendServers[0].address.Port(), uint16(8132))
		assert.Equal(t, backendServers[1].name, "konnectivity-server-1")
		assert.Equal(t, backendServers[1].address.Host(), "10.81.146.184")
		assert.Equal(t, backendServers[1].address.Port(), uint16(8132))
		assert.Equal(t, backendServers[2].name, "konnectivity-server-2")
		assert.Equal(t, backendServers[2].address.Host(), "10.81.146.254")
		assert.Equal(t, backendServers[2].address.Port(), uint16(8132))
	}
}

func Test_ShowServersState_UnknownCommand(t *testing.T) {
	output := strings.Join([]string{
		"Unknown command: 'show', but maybe one of the following ones is a better match:",
		"  show servers state [<backend>]          : dump volatile server information (all or for a single backend)",
		"  help [<command>]                        : list matching or all commands",
		"  prompt                                  : toggle interactive mode with prompt",
		"  quit                                    : disconnect",
		"",
		"",
	}, "\n")

	sendRaw := func(_ context.Context, cmd []byte) (io.ReadCloser, error) {
		require.Equal(t, []byte("show servers state\n"), cmd)
		return io.NopCloser(strings.NewReader(output)), nil
	}

	underTest := haproxySender{sendRaw}

	servers, err := underTest.ShowServersState(context.TODO())
	if assert.Error(t, err) {
		assert.Equal(t, "unknown command", err.Error())
	}
	assert.Nil(t, servers)
}
