//go:build unix

/*
Copyright 2025 k0s authors

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

package controller

import (
	"slices"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
)

func TestAvailableComponents_SortedAndUnique(t *testing.T) {
	availableComponents := availableComponents[:]
	expected := slices.Clone(availableComponents)
	slices.Sort(expected)

	assert.Equal(t, expected, availableComponents, "Available components aren't sorted")

	expected = slices.Compact(expected)
	assert.Equal(t, expected, availableComponents, "Available components contain duplicates")
}

func TestDisableComponentsFlag(t *testing.T) {
	newCmd := func() *cobra.Command {
		cmd := NewControllerCmd()
		cmd.PersistentPreRun = nil
		cmd.PersistentPreRunE = nil
		cmd.SilenceUsage = true
		cmd.RunE = func(cmd *cobra.Command, args []string) error { return nil }
		return cmd
	}

	t.Run("SupportsCommas", func(t *testing.T) {
		underTest := newCmd()
		underTest.SetArgs([]string{"--disable-components=node-role,helm"})

		assert.NoError(t, underTest.Execute())
		value := underTest.Flags().Lookup("disable-components").Value
		assert.Equal(t, "[helm,node-role]", value.String())
	})

	t.Run("SupportsMultipleFlags", func(t *testing.T) {
		underTest := newCmd()
		underTest.SetArgs([]string{"--disable-components=node-role", "--disable-components=helm"})

		assert.NoError(t, underTest.Execute())
		value := underTest.Flags().Lookup("disable-components").Value
		assert.Equal(t, "[helm,node-role]", value.String())
	})

	t.Run("RejectsUnknownComponents", func(t *testing.T) {
		underTest := newCmd()
		underTest.SetArgs([]string{"--disable-components=node-role,bogus,helm"})

		assert.ErrorContains(t, underTest.Execute(), `invalid argument "node-role,bogus,helm" for "--disable-components" flag: unknown component at index 1`)
		value := underTest.Flags().Lookup("disable-components").Value
		assert.Zero(t, value.String())
	})
}
