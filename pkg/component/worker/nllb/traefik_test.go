// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package nllb

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	k0snet "github.com/k0sproject/k0s/internal/pkg/net"
	"sigs.k8s.io/yaml"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteTraefikConfigFiles(t *testing.T) {
	for _, test := range []struct {
		name     string
		expected int
		servers  []string
	}{
		{"empty", 0, []string{}},
		{"one", 1, []string{"foo:16"}},
		{"two", 2, []string{"foo:16", "bar:17"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()

			underTest := traefikProxy{
				dir: dir,
				config: &traefikConfig{
					traefikPodParams{},
					traefikInstallConfig{
						bindIP:            net.IPv6loopback,
						apiServerBindPort: 7443,
					},
					traefikRoutingConfig{
						konnectivityServerPort: 8132,
					},
				},
			}

			for _, server := range test.servers {
				server, err := k0snet.ParseHostPort(server)
				require.NoError(t, err)
				underTest.config.apiServers = append(underTest.config.apiServers, *server)
			}

			require.NoError(t, underTest.writeInstallConfigFile())
			require.NoError(t, underTest.writeRoutingConfigFile())

			parse := func(t *testing.T, file string) (parsed map[string]any) {
				content, err := os.ReadFile(filepath.Join(dir, file))
				require.NoError(t, err)
				require.NoErrorf(t, yaml.Unmarshal(content, &parsed), "invalid YAML in %s", file)
				return
			}

			t.Run("config.yaml", func(t *testing.T) {
				address, err := evalJSONPath[string](parse(t, "config.yaml"),
					".entryPoints.apiserver.address",
				)
				require.NoError(t, err)
				assert.Equal(t, "[::1]:7443", address)
			})

			t.Run("routing.yaml", func(t *testing.T) {
				servers, err := evalJSONPath[[]any](parse(t, "routing.yaml"),
					".tcp.services.apiserver.loadBalancer.servers",
				)
				require.NoError(t, err)

				addrs := []string{}
				for _, server := range servers {
					addr, serr := evalJSONPath[string](server, ".address")
					if assert.NoError(t, serr) {
						addrs = append(addrs, addr)
					}
				}

				assert.Equal(t, test.servers, addrs)
			})
		})
	}
}
