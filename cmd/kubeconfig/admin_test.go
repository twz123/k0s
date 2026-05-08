// SPDX-FileCopyrightText: 2024 k0s authors
// SPDX-License-Identifier: Apache-2.0

package kubeconfig_test

import (
	"bytes"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"

	"github.com/k0sproject/k0s/cmd"
	"github.com/k0sproject/k0s/pkg/apis/k0s/v1beta1"
	"github.com/k0sproject/k0s/pkg/config"
	"github.com/k0sproject/k0s/pkg/constant"

	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/yaml"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAdmin(t *testing.T) {
	testDir := t.TempDir()
	dataDir := filepath.Join(testDir, "data")
	configPath := filepath.Join(testDir, "config.yaml")

	if !t.Run("init_controller", func(t *testing.T) {
		binDir := t.TempDir()
		for _, exe := range []string{"kube-apiserver", "xtables-legacy-multi", "xtables-nft-multi"} {
			require.NoError(t, os.WriteFile(filepath.Join(binDir, exe), nil, 0700))
		}
		t.Setenv("PATH", binDir)

		me, err := user.Current()
		require.NoError(t, err)

		writeYAML(t, configPath, &v1beta1.ClusterConfig{
			Spec: &v1beta1.ClusterSpec{
				Install: &v1beta1.InstallSpec{
					SystemUsers: &v1beta1.SystemUser{
						KubeAPIServer: me.Username,
						KubeScheduler: me.Username,
					},
				},
				Storage: &v1beta1.StorageSpec{
					Etcd: &v1beta1.EtcdConfig{
						ExternalCluster: &v1beta1.ExternalCluster{
							Endpoints:  []string{"127.0.0.1"},
							EtcdPrefix: t.Name(),
						},
					},
				},
				API: &v1beta1.APISpec{
					Port: 65432, ExternalAddress: "not-here.example.com",
				},
			},
		})

		var stdout bytes.Buffer
		var stderr strings.Builder
		underTest := cmd.NewRootCmd()
		underTest.SetArgs([]string{"controller",
			"--config", configPath,
			"--data-dir", dataDir,
			"--disable-components", strings.Join([]string{
				constant.KubeSchedulerComponentName,
				constant.KonnectivityServerComponentName,
			}, ","),
			"--init-only",
		})
		underTest.SetOut(&stdout)
		underTest.SetErr(&stderr)
		assert.NoError(t, underTest.Execute())
		assert.Empty(t, stderr.String())
		t.Logf("Stdout: %s", &stdout)
	}) {
		return
	}

	t.Run("kubeconfig_admin", func(t *testing.T) {
		k0sVars := &config.CfgVars{
			StartupConfigPath: configPath,
			RuntimeConfigPath: filepath.Join(dataDir, "run", "k0s.yaml"),
			DataDir:           dataDir,
			CertRootDir:       filepath.Join(dataDir, "pki"),
		}
		cfg, err := config.NewRuntimeConfig(k0sVars, nil)
		require.NoError(t, err)
		t.Cleanup(func() { assert.NoError(t, cfg.Spec.Cleanup()) })

		var stdout bytes.Buffer
		var stderr strings.Builder
		underTest := cmd.NewRootCmd()
		underTest.SetArgs([]string{"kubeconfig", "--data-dir", dataDir, "admin"})
		underTest.SetOut(&stdout)
		underTest.SetErr(&stderr)

		assert.NoError(t, underTest.Execute())

		assert.Empty(t, stderr.String())

		adminConf, err := clientcmd.Load(stdout.Bytes())
		require.NoError(t, err)

		if theCluster, ok := adminConf.Clusters["k0s"]; assert.True(t, ok, "%#v", adminConf.Clusters) {
			assert.Equal(t, "https://not-here.example.com:65432", theCluster.Server)
		}
	})
}

func TestAdmin_NoAdminConfig(t *testing.T) {
	dataDir := t.TempDir()

	configPath := filepath.Join(dataDir, "k0s.yaml")
	require.NoError(t, os.WriteFile(configPath, nil, 0644))

	k0sVars := &config.CfgVars{
		StartupConfigPath: configPath,
		RuntimeConfigPath: filepath.Join(dataDir, "run", "k0s.yaml"),
		DataDir:           dataDir,
		CertRootDir:       filepath.Join(dataDir, "pki"),
	}
	require.NoError(t, os.Mkdir(filepath.Dir(k0sVars.RuntimeConfigPath), 0700))

	cfg, err := config.NewRuntimeConfig(k0sVars, nil)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, cfg.Spec.Cleanup()) })

	var stdout, stderr strings.Builder
	underTest := cmd.NewRootCmd()
	underTest.SetArgs([]string{"kubeconfig", "--data-dir", dataDir, "admin"})
	underTest.SetOut(&stdout)
	underTest.SetErr(&stderr)

	assert.Error(t, underTest.Execute())

	assert.Empty(t, stdout.String())
	msg := fmt.Sprintf("admin PKI file %q not found, check if the control plane is initialized on this node", filepath.Join(dataDir, "pki", "admin.crt"))
	assert.Equal(t, "Error: "+msg+"\n", stderr.String())
}

func writeYAML(t *testing.T, path string, data any) {
	bytes, err := yaml.Marshal(data)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
	require.NoError(t, os.WriteFile(path, bytes, 0644))
}
