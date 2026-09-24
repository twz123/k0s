// SPDX-FileCopyrightText: 2026 k0s authors
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"os"
	"path/filepath"
	"runtime"
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
	certRoot := func(name string) string { return filepath.Join(k0sVars.CertRootDir, name) }
	run := func(name string) string { return filepath.Join(k0sVars.RunDir, name) }
	ca, _ := newTestKubeletServingCA(t)

	buildArgs := func(extraArgs string, clusterConfig *v1beta1.ClusterConfig) stringmap.StringMap {
		manager := Manager{K0sVars: k0sVars, ExtraArgs: extraArgs, KubeletServingCA: ca}
		return manager.buildArgs(logrus.WithField("test", t.Name()), clusterConfig)
	}

	allSigners := []string{"kubelet-serving", "kubelet-client", "kube-apiserver-client", "legacy-unknown"}
	otherSigners := allSigners[1:]

	t.Run("signs kubelet serving certs with the kubelet-serving CA", func(t *testing.T) {
		args := buildArgs("", v1beta1.DefaultClusterConfig())
		assert.Equal(t, run(kubeletServingCACertFile), args["cluster-signing-kubelet-serving-cert-file"], "Kubelet-serving signer should use the kubelet-serving CA")
		assert.Equal(t, run(kubeletServingCAKeyFile), args["cluster-signing-kubelet-serving-key-file"], "Kubelet-serving signer should use the kubelet-serving CA")
		for _, signer := range otherSigners {
			assert.Equal(t, certRoot("ca.crt"), args["cluster-signing-"+signer+"-cert-file"], "Other signers should use the cluster CA")
			assert.Equal(t, certRoot("ca.key"), args["cluster-signing-"+signer+"-key-file"], "Other signers should use the cluster CA")
		}
		assert.NotContains(t, args, "cluster-signing-cert-file", "Catch-all flags can't be combined with per-signer ones")
		assert.NotContains(t, args, "cluster-signing-key-file", "Catch-all flags can't be combined with per-signer ones")
	})

	t.Run("signs all certs with the cluster CA without a kubelet-serving CA", func(t *testing.T) {
		manager := Manager{K0sVars: k0sVars}
		args := manager.buildArgs(logrus.WithField("test", t.Name()), v1beta1.DefaultClusterConfig())
		assert.Equal(t, certRoot("ca.crt"), args["cluster-signing-cert-file"], "All signers should use the cluster CA")
		assert.Equal(t, certRoot("ca.key"), args["cluster-signing-key-file"], "All signers should use the cluster CA")
		for _, signer := range allSigners {
			assert.NotContains(t, args, "cluster-signing-"+signer+"-cert-file", "Per-signer flags can't be combined with catch-all ones")
			assert.NotContains(t, args, "cluster-signing-"+signer+"-key-file", "Per-signer flags can't be combined with catch-all ones")
		}
	})

	t.Run("honors a user-provided catch-all", func(t *testing.T) {
		for name, build := range map[string]func() stringmap.StringMap{
			"CLI": func() stringmap.StringMap {
				return buildArgs("--cluster-signing-cert-file=/some/where/else.crt --cluster-signing-key-file=/some/where/else.key", v1beta1.DefaultClusterConfig())
			},
			"extra args": func() stringmap.StringMap {
				clusterConfig := v1beta1.DefaultClusterConfig()
				clusterConfig.Spec.ControllerManager.ExtraArgs = map[string]string{
					"cluster-signing-cert-file": "/some/where/else.crt",
					"cluster-signing-key-file":  "/some/where/else.key",
				}
				return buildArgs("", clusterConfig)
			},
			"raw args": func() stringmap.StringMap {
				clusterConfig := v1beta1.DefaultClusterConfig()
				clusterConfig.Spec.ControllerManager.RawArgs = []string{"--cluster-signing-cert-file=/some/where/else.crt", "--cluster-signing-key-file", "/some/where/else.key"}
				return buildArgs("", clusterConfig)
			},
		} {
			t.Run(name, func(t *testing.T) {
				args := build()
				for _, signer := range append([]string{"kubelet-serving"}, otherSigners...) {
					assert.NotContains(t, args, "cluster-signing-"+signer+"-cert-file", "Per-signer flags can't be combined with catch-all ones")
					assert.NotContains(t, args, "cluster-signing-"+signer+"-key-file", "Per-signer flags can't be combined with catch-all ones")
				}
			})
		}
	})

	t.Run("completes a partial catch-all with the cluster CA", func(t *testing.T) {
		clusterConfig := v1beta1.DefaultClusterConfig()
		clusterConfig.Spec.ControllerManager.ExtraArgs = map[string]string{"cluster-signing-cert-file": "/some/where/else.crt"}
		args := buildArgs("", clusterConfig)
		assert.Equal(t, "/some/where/else.crt", args["cluster-signing-cert-file"], "User-provided cert should be used")
		assert.Equal(t, certRoot("ca.key"), args["cluster-signing-key-file"], "Missing key should be filled in with the cluster CA's")
		assert.NotContains(t, args, "cluster-signing-kubelet-serving-cert-file", "Per-signer flags can't be combined with catch-all ones")
	})
}

func TestManagerWritesKubeletServingCA(t *testing.T) {
	k0sVars, err := config.NewCfgVars(nil, t.TempDir())
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(k0sVars.RunDir, 0755))
	ca, _ := newTestKubeletServingCA(t)
	expectedKey, err := ca.KeyPEM()
	require.NoError(t, err)

	t.Run("writes nothing without a kubelet-serving CA", func(t *testing.T) {
		manager := Manager{K0sVars: k0sVars, uid: 1234}
		require.NoError(t, manager.writeKubeletServingCA())
		entries, err := os.ReadDir(k0sVars.RunDir)
		require.NoError(t, err)
		assert.Empty(t, entries, "No files should have been written")
	})

	manager := Manager{K0sVars: k0sVars, KubeletServingCA: ca, uid: 1234}
	require.NoError(t, manager.writeKubeletServingCA())

	for name, expected := range map[string][]byte{
		kubeletServingCAKeyFile:  expectedKey,
		kubeletServingCACertFile: ca.CertPEM(),
	} {
		content, err := os.ReadFile(filepath.Join(k0sVars.RunDir, name))
		if assert.NoError(t, err, "%s should have been written", name) {
			assert.Equal(t, string(expected), string(content), "Unexpected content in %s", name)
		}
	}

	if runtime.GOOS == "windows" {
		return // No UNIX-style permissions on Windows
	}
	for name, mode := range map[string]os.FileMode{
		kubeletServingCAKeyFile:  0640,
		kubeletServingCACertFile: 0644,
	} {
		if info, err := os.Stat(filepath.Join(k0sVars.RunDir, name)); assert.NoError(t, err) {
			assert.Equal(t, mode, info.Mode().Perm(), "Unexpected permissions on %s", name)
		}
	}
}
