//go:build windows

// SPDX-FileCopyrightText: 2025 k0s authors
// SPDX-License-Identifier: Apache-2.0

package dism_test

import (
	"testing"

	"github.com/k0sproject/k0s/internal/os/windows/dism"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_ContainersFeatureExists(t *testing.T) {
	api, err := dism.Initialize()
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, api.Shutdown()) })

	session, err := api.OpenOnlineSession()
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, session.Close()) })

	info, err := session.GetFeatureInfo("Containers")
	require.NoError(t, err)

	t.Logf("Containers: %#+v", info)
	t.Error("For zhe logz")
}
