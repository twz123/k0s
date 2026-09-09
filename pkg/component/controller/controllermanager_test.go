// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"path/filepath"
	"testing"

	"github.com/k0sproject/k0s/internal/pkg/stringmap"
	"github.com/k0sproject/k0s/pkg/apis/k0s/v1beta1"
	"github.com/k0sproject/k0s/pkg/config"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestManagerArgs(t *testing.T) {
	k0sVars, err := config.NewCfgVars(nil, t.TempDir())
	require.NoError(t, err)
	pki := func(name string) string { return filepath.Join(k0sVars.CertRootDir, name) }

	buildArgs := func(extraArgs string, clusterConfig *v1beta1.ClusterConfig) stringmap.StringMap {
		manager := Manager{K0sVars: k0sVars, ExtraArgs: extraArgs}
		return manager.buildArgs(logrus.WithField("test", t.Name()), clusterConfig)
	}

	t.Run("signs with the cluster CA", func(t *testing.T) {
		args := buildArgs("", v1beta1.DefaultClusterConfig())
		assert.Equal(t, pki("ca.crt"), args["cluster-signing-cert-file"])
		assert.Equal(t, pki("ca.key"), args["cluster-signing-key-file"])
	})

	t.Run("passes overrides through", func(t *testing.T) {
		clusterConfig := v1beta1.DefaultClusterConfig()
		clusterConfig.Spec.ControllerManager.ExtraArgs = map[string]string{"cluster-signing-key-file": "/some/where/else.key"}
		args := buildArgs("--cluster-signing-cert-file=/some/where/else.crt", clusterConfig)
		assert.Equal(t, "/some/where/else.crt", args["cluster-signing-cert-file"])
		assert.Equal(t, "/some/where/else.key", args["cluster-signing-key-file"])
	})
}
