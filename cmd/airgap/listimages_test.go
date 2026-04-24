// SPDX-FileCopyrightText: 2021 k0s authors
// SPDX-License-Identifier: Apache-2.0

package airgap_test

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/k0sproject/k0s/cmd"
	internalio "github.com/k0sproject/k0s/internal/io"
	k0sv1beta1 "github.com/k0sproject/k0s/pkg/apis/k0s/v1beta1"

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

	defaults := k0sv1beta1.DefaultClusterImages()
	defaultEnvoyImage := k0sv1beta1.DefaultEnvoyProxyImage().URI()

	t.Run("HonorsIOErrors", func(t *testing.T) {
		var args []string
		switch runtime.GOOS {
		case "linux", "windows":
		default:
			// Hard-code the platform when testing on platforms that won't list any images
			args = slices.Insert(args, 0, "--platform=linux/amd64")
		}

		var writes uint
		underTest, _, stderr := newAirgapListImagesCmdWithConfig(t, "", args...)
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
		tests := []struct {
			name                    string
			args                    []string
			contained, notContained []string
		}{
			{
				name: "linux-amd64",
				args: []string{"--all", "--platform=linux/amd64"},
				contained: []string{
					defaults.KubeProxy.URI(),
					defaults.Pause.URI(),
					defaults.KubeRouter.CNI.URI(),
					defaults.Calico.CNI.URI(),
					defaults.PushGateway.URI(),
					defaultEnvoyImage,
				},
				notContained: []string{
					defaults.Windows.KubeProxy.URI(),
				},
			},
			{
				name: "linux-arm-v7",
				args: []string{"--all", "--platform=linux/arm/v7"},
				contained: []string{
					defaults.KubeProxy.URI(),
					defaults.PushGateway.URI(),
				},
				notContained: []string{
					defaultEnvoyImage,
				},
			},
			{
				name: "windows-amd64",
				args: []string{"--all", "--platform=windows/amd64"},
				contained: []string{
					defaults.Windows.KubeProxy.URI(),
					defaults.Windows.Pause.URI(),
					defaults.Calico.Windows.CNI.URI(),
					defaults.Calico.Windows.Node.URI(),
				},
				notContained: []string{
					defaults.KubeProxy.URI(),
					defaults.Pause.URI(),
					defaults.CoreDNS.URI(),
					defaults.KubeRouter.CNI.URI(),
					defaultEnvoyImage,
				},
			},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				underTest, out, err := newAirgapListImagesCmdWithConfig(t, "{}", test.args...)

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

	t.Run("Defaults", func(t *testing.T) {
		tests := []struct {
			name                    string
			args                    []string
			contained, notContained []string
		}{
			{
				name: "linux-amd64",
				args: []string{"--platform=linux/amd64"},
				contained: []string{
					defaults.KubeProxy.URI(),
					defaults.KubeRouter.CNI.URI(),
				},
				notContained: []string{
					defaults.Calico.CNI.URI(),
					defaults.PushGateway.URI(),
					defaultEnvoyImage,
				},
			},
			{
				name: "windows-amd64",
				args: []string{"--platform=windows/amd64"},
				contained: []string{
					defaults.Windows.KubeProxy.URI(),
					defaults.Windows.Pause.URI(),
				},
				notContained: []string{
					defaults.Calico.Windows.CNI.URI(),
					defaults.KubeProxy.URI(),
				},
			},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				underTest, out, err := newAirgapListImagesCmdWithConfig(t, "{}", test.args...)

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

	t.Run("NodeLocalLoadBalancing", func(t *testing.T) {
		defaultTraefikImage := k0sv1beta1.DefaultTraefikImage().URI()

		const (
			customImage = "registry.example.com/custom:v1337"
			//nolint:dupword
			yamlData = `
apiVersion: k0s.k0sproject.io/v1beta1
kind: ClusterConfig
spec:
  network:
    nodeLocalLoadBalancing:
      enabled: %t
      type: %s
      %s:
        image:
          image: registry.example.com/custom
          version: v1337`
		)

		for _, test := range []struct {
			backend                 string
			name                    string
			enabled                 bool
			args                    []string
			contained, notContained []string
		}{
			{"envoyProxy", "enabled-linux-amd64", true, []string{"--platform=linux/amd64"}, []string{customImage}, []string{defaultEnvoyImage}},
			{"envoyProxy", "enabled-linux-arm-v7", true, []string{"--platform=linux/arm/v7"}, nil, []string{customImage, defaultEnvoyImage}},
			{"envoyProxy", "enabled-windows-amd64", true, []string{"--platform=windows/amd64"}, nil, []string{customImage, defaultEnvoyImage}},
			{"envoyProxy", "disabled-linux-amd64", false, []string{"--platform=linux/amd64"}, nil, []string{customImage, defaultEnvoyImage}},
			{"traefik", "enabled", true, nil, []string{customImage}, []string{defaultTraefikImage}},
			{"traefik", "disabled", false, nil, nil, []string{customImage, defaultTraefikImage}},
		} {
			t.Run(test.backend+"-"+test.name, func(t *testing.T) {
				nllbType := strings.ToUpper(test.backend[:1]) + test.backend[1:]
				config := fmt.Sprintf(yamlData, test.enabled, nllbType, test.backend)
				underTest, out, err := newAirgapListImagesCmdWithConfig(t, config, test.args...)

				require.NoError(t, underTest.Execute(), config)

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
