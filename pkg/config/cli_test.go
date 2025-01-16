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

package config

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAvailableComponents_SortedAndUnique(t *testing.T) {
	availableComponents := availableComponents[:]
	expected := slices.Clone(availableComponents)
	slices.Sort(expected)

	assert.Equal(t, expected, availableComponents, "Available components aren't sorted")

	expected = slices.Compact(expected)
	assert.Equal(t, expected, availableComponents, "Available components contain duplicates")
}

func TestControllerOptions_DisableComponents(t *testing.T) {
	require.LessOrEqual(t,
		uintptr(len(availableComponents)),
		reflect.TypeFor[componentSetFlag]().Size()*8,
		"Number of available components is exceeding bitset size",
	)

	t.Run("failsOnUnknownComponent", func(t *testing.T) {
		flags := GetControllerFlags(&ControllerOptions{})
		value := flags.Lookup("disable-components").Value

		t.Run("single", func(t *testing.T) {
			err := flags.Parse([]string{"--disable-components=bogus"})
			assert.ErrorContains(t, err, `invalid argument "bogus" for "--disable-components" flag: unknown component`)
			assert.Empty(t, value.String())
		})

		t.Run("withCommas", func(t *testing.T) {
			err := flags.Parse([]string{"--disable-components=node-role,bogus,helm"})
			assert.ErrorContains(t, err, `invalid argument "node-role,bogus,helm" for "--disable-components" flag: unknown component at index 1`)
			assert.Empty(t, value.String())
		})
	})

	t.Run("removesDuplicateComponents", func(t *testing.T) {
		disabled := []string{"helm", "kube-proxy", "coredns", "kube-proxy", "autopilot"}
		expected := "autopilot,coredns,helm,kube-proxy"

		for _, test := range []struct {
			name string
			args []string
		}{
			{"separate", (func() (args []string) {
				for _, disabled := range disabled {
					args = append(args, "--disable-components", disabled)
				}
				return
			})()},

			{"withCommas", []string{"--disable-components", strings.Join(disabled, ",")}},
		} {
			t.Run(test.name, func(t *testing.T) {
				var underTest ControllerOptions
				flags := GetControllerFlags(&underTest)
				value := flags.Lookup("disable-components").Value

				err := flags.Parse(test.args)

				assert.NoError(t, err)
				assert.Equal(t, expected, value.String())
				for _, disabled := range disabled {
					assert.True(t, underTest.IsComponentDisabled(disabled))
				}
				assert.False(t, underTest.IsComponentDisabled("node-role"))
				assert.False(t, underTest.IsComponentDisabled("bogus"))
			})
		}
	})
}

func TestLogLevelsFlagSet(t *testing.T) {
	t.Run("full_input", func(t *testing.T) {
		var underTest logLevelsFlag
		assert.NoError(t, underTest.Set("kubelet=a,kube-scheduler=b,kube-controller-manager=c,kube-apiserver=d,konnectivity-server=e,etcd=f,containerd=g"))
		assert.Equal(t, logLevelsFlag{
			Containerd:            "g",
			Etcd:                  "f",
			Konnectivity:          "e",
			KubeAPIServer:         "d",
			KubeControllerManager: "c",
			KubeScheduler:         "b",
			Kubelet:               "a",
		}, underTest)
		assert.Equal(t, "[containerd=g,etcd=f,konnectivity-server=e,kube-apiserver=d,kube-controller-manager=c,kube-scheduler=b,kubelet=a]", underTest.String())
	})

	t.Run("partial_input", func(t *testing.T) {
		var underTest logLevelsFlag
		assert.NoError(t, underTest.Set("[kubelet=a,etcd=b]"))
		assert.Equal(t, logLevelsFlag{
			Containerd:            "info",
			Etcd:                  "b",
			Konnectivity:          "1",
			KubeAPIServer:         "1",
			KubeControllerManager: "1",
			KubeScheduler:         "1",
			Kubelet:               "a",
		}, underTest)
	})

	for _, test := range []struct {
		name, input, msg string
	}{
		{"unknown_component", "random=debug", `unknown component name: "random"`},
		{"empty_component_name", "=info", "component name cannot be empty"},
		{"unknown_component_only", "random", `must be of format component=level: "random"`},
		{"no_equals", "kube-apiserver", `must be of format component=level: "kube-apiserver"`},
		{"mixed_valid_and_invalid", "containerd=info,random=debug", `unknown component name: "random"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			var underTest logLevelsFlag
			assert.ErrorContains(t, underTest.Set(test.input), test.msg)
		})
	}
}
