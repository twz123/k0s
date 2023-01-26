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
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	k0snet "github.com/k0sproject/k0s/internal/pkg/net"
	"github.com/sirupsen/logrus"

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
			underTest := haproxy{
				configDir: configDir,
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
		})
	}
}

func Test_ShowServersState_Success(t *testing.T) {
	newHostPort := func(host string, port uint16) k0snet.HostPort {
		hostPort, err := k0snet.NewHostPort(host, port)
		require.NoError(t, err)
		return *hostPort
	}
	serversState := strings.Join([]string{
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
	expectedUpstreamAPIServers := map[string]serverState{
		"apiserver-0": {newHostPort("10.81.146.113", 6443), false, true},
		"apiserver-1": {newHostPort("10.81.146.184", 6443), false, true},
		"apiserver-2": {newHostPort("10.81.146.254", 6443), false, true},
	}
	expectedUpstreamKonnectivityServers := map[string]serverState{
		"konnectivity-server-0": {newHostPort("10.81.146.113", 8132), false, false},
		"konnectivity-server-1": {newHostPort("10.81.146.184", 8132), false, false},
		"konnectivity-server-2": {newHostPort("10.81.146.254", 8132), false, false},
	}

	sendRaw := func(_ context.Context, cmd []byte) (io.ReadCloser, error) {
		require.Equal(t, []byte("show servers state\n"), cmd)
		return io.NopCloser(strings.NewReader(serversState)), nil
	}

	underTest := haproxySender{
		logrus.StandardLogger().WithField("test", t.Name()),
		sendRaw,
	}

	servers, err := underTest.ShowServersState(context.TODO())
	require.NoError(t, err)
	assert.Len(t, servers, 2)
	assert.Equal(t, expectedUpstreamAPIServers, servers["upstream_apiservers"])
	assert.Equal(t, expectedUpstreamKonnectivityServers, servers["upstream_konnectivity_servers"])
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

	underTest := haproxySender{
		logrus.StandardLogger().WithField("test", t.Name()),
		sendRaw,
	}

	servers, err := underTest.ShowServersState(context.TODO())
	if assert.Error(t, err) {
		assert.Equal(t, "unknown command", err.Error())
	}
	assert.Nil(t, servers)
}
