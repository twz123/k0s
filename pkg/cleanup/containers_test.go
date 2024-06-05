package cleanup

import (
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/BurntSushi/toml"
	"github.com/k0sproject/k0s/pkg/config"
	containerruntime "github.com/k0sproject/k0s/pkg/container/runtime"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestYolo(t *testing.T) {
	logrus.SetLevel(logrus.DebugLevel)

	tmpDir := t.TempDir()
	dataDir := filepath.Join(tmpDir, "data")
	runDir := filepath.Join(tmpDir, "run")
	socketPath := filepath.Join(tmpDir, "containerd.sock")
	confPath := filepath.Join(tmpDir, "containerd.toml")
	cniBinDir := filepath.Join(tmpDir, "cni", "libexec")
	cniConfDir := filepath.Join(tmpDir, "cni", "etc", "net.d")

	require.NoError(t, os.MkdirAll(cniBinDir, 0755))
	require.NoError(t, os.MkdirAll(cniConfDir, 0755))

	id := map[string]any{"uid": os.Getuid(), "gid": os.Getgid()}
	confData, err := toml.Marshal(map[string]any{
		"version": 2,
		"debug":   id, "ttrpc": id, "grpc": id,
		"plugins": map[string]any{
			"io.containerd.grpc.v1.cri": map[string]any{
				"cni": map[string]any{
					"bin_dir":  cniBinDir,
					"conf_dir": cniConfDir,
				},
			},
		},
	})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(confPath, confData, 0640))

	var binPath string
	switch runtime.GOOS {
	case "windows":
		binPath = filepath.Join("windows", "bin", "containerd.exe")
	default:
		binPath = filepath.Join("linux", "bin", "containerd")
	}
	binPath = filepath.Join("..", "..", "embedded-bins", "staging", binPath)
	binPath, err = filepath.Abs(binPath)
	require.NoError(t, err)

	c := containers{Config: &Config{
		cfgFile: "",
		containerd: &containerdConfig{
			binPath:    binPath,
			socketPath: socketPath,
			configPath: confPath,
		},
		containerRuntime: containerruntime.NewContainerRuntime(&url.URL{Scheme: "unix", OmitHost: true, Path: filepath.ToSlash(socketPath)}),
		dataDir:          dataDir,
		k0sVars:          &config.CfgVars{},
		runDir:           runDir,
	}}

	assert.NoError(t, c.Run())
}
