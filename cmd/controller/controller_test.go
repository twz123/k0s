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

package controller_test

import (
	"io"
	"testing"

	"github.com/k0sproject/k0s/cmd/controller"

	"github.com/spf13/cobra"

	"github.com/stretchr/testify/assert"
)

func TestControllerCmd_Args(t *testing.T) {

	newCommand := func(args ...string) *cobra.Command {
		c := controller.NewControllerCmd()
		c.SetArgs(args)
		c.SetOut(io.Discard)
		c.SetErr(io.Discard)
		return c
	}

	t.Run("none", func(t *testing.T) {
		var ok bool
		underTest := newCommand()
		underTest.RunE = func(cmd *cobra.Command, args []string) error { ok = true; return nil }

		assert.NoError(t, underTest.Execute())
		assert.True(t, ok, "RunE has not been called")
	})

	t.Run("with join token", func(t *testing.T) {
		var ok bool
		underTest := newCommand("the-join-token")
		underTest.RunE = func(cmd *cobra.Command, args []string) error { ok = true; return nil }

		assert.NoError(t, underTest.Execute())
		assert.True(t, ok, "RunE has not been called")
	})

	t.Run("with bogus extra arg", func(t *testing.T) {
		underTest := newCommand("the-join-token", "u-no-pass-more-than-one-arg")
		underTest.RunE = func(cmd *cobra.Command, args []string) error {
			assert.Fail(t, "RunE has been called")
			return nil
		}

		assert.Error(t, underTest.Execute())
	})
}
