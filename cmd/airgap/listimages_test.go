// SPDX-FileCopyrightText: 2021 k0s authors
// SPDX-License-Identifier: Apache-2.0

package airgap_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/k0sproject/k0s/cmd"
	internalio "github.com/k0sproject/k0s/internal/io"
	k0sv1beta1 "github.com/k0sproject/k0s/pkg/apis/k0s/v1beta1"
	"github.com/k0sproject/k0s/pkg/component/worker/nllb"

	"github.com/spf13/cobra"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAirgapListImages(t *testing.T) {
	// TODO: k0s will always try to read the runtime config file first
	// (/run/k0s/k0s.yaml). There's currently no knob to change that (maybe use
	// XDG_RUNTIME_DIR, XDG_STATE_HOME, XDG_DATA_HOME?). If the file is present
	// on a host executing this test, it will interfere with it.
	require.NoFileExists(t, "/run/k0s/k0s.yaml", "Runtime config exists and will interfere with this test.")

	t.Run("HonorsIOErrors", func(t *testing.T) {
		var writes uint
		underTest, _, stderr := newAirgapListImagesCmdWithConfig(t, "")
		underTest.SilenceUsage = true // Cobra writes usage to stdout on errors 🤔
		underTest.SetOut(internalio.WriterFunc(func(p []byte) (int, error) {
			writes++
			return 0, assert.AnError
		}))

		assert.Same(t, assert.AnError, underTest.Execute())
		assert.Equal(t, uint(1), writes, "Expected a single write to stdout")
		assert.Equal(t, fmt.Sprintf("Error: %v\n", assert.AnError), stderr.String())
	})

	t.Run("All", func(t *testing.T) {
		defaultEnvoyImage := k0sv1beta1.DefaultEnvoyProxyImage().URI()

		underTest, out, err := newAirgapListImagesCmdWithConfig(t, "{}", "--all")

		require.NoError(t, underTest.Execute())
		lines := strings.Split(out.String(), "\n")
		if nllb.EnvoySupported {
			assert.Contains(t, lines, defaultEnvoyImage)
		} else {
			assert.NotContains(t, lines, defaultEnvoyImage)
		}
		assert.Contains(t, lines, k0sv1beta1.DefaultTraefikProxyImage().URI())

		assert.Empty(t, err.String())
	})

	t.Run("NodeLocalLoadBalancing", func(t *testing.T) {
		const (
			customImage = "example.com/envoy:v1337"
			//nolint:dupword // This is YAML data!
			yamlData = `apiVersion: k0s.k0sproject.io/v1beta1
kind: ClusterConfig
spec:
  network:
    nodeLocalLoadBalancing:
      enabled: %t
      envoyProxy:
        image:
          image: example.com/envoy
          version: v1337`
		)
		defaultEnvoyImage := k0sv1beta1.DefaultEnvoyProxyImage().URI()

		for _, test := range []struct {
			name                    string
			enabled                 bool
			contained, notContained []string
		}{
			{"enabled", true, []string{customImage}, []string{defaultEnvoyImage}},
			{"disabled", false, nil, []string{customImage, defaultEnvoyImage}},
		} {
			t.Run(test.name, func(t *testing.T) {
				underTest, out, err := newAirgapListImagesCmdWithConfig(t, fmt.Sprintf(yamlData, test.enabled))

				require.NoError(t, underTest.Execute())

				lines := strings.Split(out.String(), "\n")
				for _, contained := range test.contained {
					if nllb.EnvoySupported {
						assert.Contains(t, lines, contained)
					} else {
						assert.NotContains(t, lines, contained)
					}
				}
				for _, notContained := range test.notContained {
					assert.NotContains(t, lines, notContained)
				}
				assert.Empty(t, err.String())
			})
		}
	})

	t.Run("NodeLocalLoadBalancingTraefik", func(t *testing.T) {
		const (
			customImage = "example.com/traefik:v1337"
			//nolint:dupword // This is YAML data!
			yamlData = `apiVersion: k0s.k0sproject.io/v1beta1
kind: ClusterConfig
spec:
  network:
    nodeLocalLoadBalancing:
      enabled: %t
      type: TraefikProxy
      traefikProxy:
        image:
          image: example.com/traefik
          version: v1337`
		)
		defaultTraefikImage := k0sv1beta1.DefaultTraefikProxyImage().URI()

		for _, test := range []struct {
			name                    string
			enabled                 bool
			contained, notContained []string
		}{
			{"enabled", true, []string{customImage}, []string{defaultTraefikImage}},
			{"disabled", false, nil, []string{customImage, defaultTraefikImage}},
		} {
			t.Run(test.name, func(t *testing.T) {
				underTest, out, err := newAirgapListImagesCmdWithConfig(t, fmt.Sprintf(yamlData, test.enabled))

				require.NoError(t, underTest.Execute())

				lines := strings.Split(out.String(), "\n")
				for _, contained := range test.contained {
					assert.Contains(t, lines, contained)
				}
				for _, notContained := range test.notContained {
					assert.NotContains(t, lines, notContained)
				}
				assert.Empty(t, err.String())
			})
		}
	})
}

func newAirgapListImagesCmdWithConfig(t *testing.T, config string, args ...string) (_ *cobra.Command, out, err *strings.Builder) {
	configFile := filepath.Join(t.TempDir(), "k0s.yaml")
	require.NoError(t, os.WriteFile(configFile, []byte(config), 0644))

	out, err = new(strings.Builder), new(strings.Builder)
	cmd := cmd.NewRootCmd()
	cmd.SetArgs(append([]string{"airgap", "--config=" + configFile, "list-images"}, args...))
	cmd.SetIn(iotest.ErrReader(errors.New("unexpected read from standard input")))
	cmd.SetOut(out)
	cmd.SetErr(err)
	return cmd, out, err
}
