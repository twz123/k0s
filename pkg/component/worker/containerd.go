/*
Copyright 2020 k0s authors

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

package worker

import (
	"bufio"
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/sirupsen/logrus"
	"golang.org/x/sync/errgroup"

	"github.com/k0sproject/k0s/internal/pkg/dir"
	"github.com/k0sproject/k0s/internal/pkg/file"
	"github.com/k0sproject/k0s/internal/pkg/templatewriter"
	"github.com/k0sproject/k0s/pkg/assets"
	"github.com/k0sproject/k0s/pkg/component/manager"
	workerconfig "github.com/k0sproject/k0s/pkg/component/worker/config"
	"github.com/k0sproject/k0s/pkg/component/worker/containerd"
	"github.com/k0sproject/k0s/pkg/config"
	"github.com/k0sproject/k0s/pkg/constant"
	"github.com/k0sproject/k0s/pkg/debounce"
	"github.com/k0sproject/k0s/pkg/supervisor"
)

const magicMarker = "# k0s_managed=true"

const confTmpl = `
# k0s_managed=true
# This is a placeholder configuration for k0s managed containerD. 
# If you wish to override the config, remove the first line and replace this file with your custom configuration.
# For reference see https://github.com/containerd/containerd/blob/main/docs/man/containerd-config.toml.5.md
version = 2
imports = [
	{{- range $i := .Imports }}
	"{{ $i }}",
	{{- end }}
]
`
const confPathPosix = "/etc/k0s/containerd.toml"
const confPathWindows = "C:\\Program Files\\containerd\\config.toml"

const importsPathPosix = "/etc/k0s/containerd.d/"
const importsPathWindows = "C:\\etc\\k0s\\containerd.d\\"

// ContainerD implement the component interface to manage containerd as k0s component
type ContainerD struct {
	supervisor    supervisor.Supervisor
	LogLevel      string
	K0sVars       *config.CfgVars
	Profile       *workerconfig.Profile
	binaries      []string
	OCIBundlePath string
	confPath      string
	importsPath   string
}

func NewContainerd(logLevel string, vars *config.CfgVars, profile *workerconfig.Profile) *ContainerD {
	c := &ContainerD{
		LogLevel: logLevel,
		K0sVars:  vars,
		Profile:  profile,
	}

	if runtime.GOOS == "windows" {
		c.binaries = []string{"containerd.exe", "containerd-shim-runhcs-v1.exe"}
		c.confPath = confPathWindows
		c.importsPath = importsPathWindows
	} else {
		c.binaries = []string{"containerd", "containerd-shim", "containerd-shim-runc-v1", "containerd-shim-runc-v2", "runc"}
		c.confPath = confPathPosix
		c.importsPath = importsPathPosix
	}
	return c
}

var _ manager.Component = (*ContainerD)(nil)

// Init extracts the needed binaries
func (c *ContainerD) Init(ctx context.Context) error {
	g, _ := errgroup.WithContext(ctx)
	for _, bin := range c.binaries {
		b := bin
		g.Go(func() error {
			return assets.Stage(c.K0sVars.BinDir, b, constant.BinDirMode)
		})
	}
	if err := c.windowsInit(); err != nil {
		return fmt.Errorf("windows init failed: %w", err)
	}
	return g.Wait()
}

func (c *ContainerD) windowsInit() error {
	if runtime.GOOS != "windows" {
		return nil
	}
	// On windows we need always run containerd.exe as a service
	// https://kubernetes.io/docs/tasks/configure-pod-container/create-hostprocess-pod/#troubleshooting-hostprocess-containers
	command := fmt.Sprintf("if (-not (Get-Service -Name containerd -ErrorAction SilentlyContinue)) { %s\\containerd.exe --register-service}", c.K0sVars.BinDir)
	return winExecute(command)
}

// Run runs containerD
func (c *ContainerD) Start(ctx context.Context) error {
	logrus.Info("Starting containerD")

	if err := c.setupConfig(); err != nil {
		return fmt.Errorf("failed to setup containerd config: %w", err)
	}
	if runtime.GOOS == "windows" {
		if err := c.windowsStart(ctx); err != nil {
			return fmt.Errorf("failed to start windows server: %w", err)
		}
	} else {
		c.supervisor = supervisor.Supervisor{
			Name:    "containerd",
			BinPath: assets.BinPath("containerd", c.K0sVars.BinDir),
			RunDir:  c.K0sVars.RunDir,
			DataDir: c.K0sVars.DataDir,
			Args: []string{
				fmt.Sprintf("--root=%s", filepath.Join(c.K0sVars.DataDir, "containerd")),
				fmt.Sprintf("--state=%s", filepath.Join(c.K0sVars.RunDir, "containerd")),
				fmt.Sprintf("--address=%s", filepath.Join(c.K0sVars.RunDir, "containerd.sock")),
				fmt.Sprintf("--log-level=%s", c.LogLevel),
				fmt.Sprintf("--config=%s", c.confPath),
			},
		}

		if err := c.supervisor.Supervise(); err != nil {
			return err
		}
	}

	go c.watchDropinConfigs(ctx)

	return nil
}

func (c *ContainerD) windowsStart(_ context.Context) error {
	if err := winExecute("Start-Service containerd"); err != nil {
		return fmt.Errorf("failed to start Windows Service %q: %w", "containerd", err)
	}
	return nil
}

func (c *ContainerD) windowsStop() error {
	if err := winExecute("Stop-Service containerd"); err != nil {
		return fmt.Errorf("failed to stop Windows Service %q: %w", "containerd", err)
	}
	return nil
}

func (c *ContainerD) setupConfig() error {
	// Check if the config file is user managed
	// If it is, we should not touch it

	k0sManaged, err := isK0sManagedConfig(c.confPath)
	if err != nil {
		return err
	}

	if !k0sManaged {
		logrus.Infof("containerd config file %s is not k0s managed, skipping config generation", c.confPath)
		return nil
	}
	if err := dir.Init(filepath.Dir(c.confPath), 0755); err != nil {
		return fmt.Errorf("can't create containerd config dir: %w", err)
	}
	if err := dir.Init(filepath.Dir(c.importsPath), 0755); err != nil {
		return fmt.Errorf("can't create containerd config imports dir: %w", err)
	}
	containerDConfigurer := containerd.NewConfigurer(c.Profile.PauseImage, filepath.Join(c.importsPath, "*.toml"))

	imports, err := containerDConfigurer.HandleImports()
	if err != nil {
		return fmt.Errorf("can't handle imports: %w", err)
	}
	output := bytes.NewBuffer([]byte{})
	tw := templatewriter.TemplateWriter{
		Name:     "containerdconfig",
		Template: confTmpl,
		Data: struct {
			Imports []string
		}{
			Imports: imports,
		},
	}
	if err := tw.WriteToBuffer(output); err != nil {
		return fmt.Errorf("can't create containerd config: %w", err)
	}
	return file.WriteContentAtomically(c.confPath, output.Bytes(), 0644)
}

func (c *ContainerD) watchDropinConfigs(ctx context.Context) {
	log := logrus.WithField("component", "containerd")
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.WithError(err).Error("failed to create watcher for drop-ins")
		return
	}
	defer watcher.Close()

	err = watcher.Add(c.importsPath)
	if err != nil {
		log.WithError(err).Error("failed to watch for drop-ins")
		return
	}

	debouncer := debounce.Debouncer[fsnotify.Event]{
		Input:   watcher.Events,
		Timeout: 3 * time.Second,
		Filter: func(item fsnotify.Event) bool {
			switch item.Op {
			case fsnotify.Create, fsnotify.Remove, fsnotify.Write, fsnotify.Rename:
				return true
			default:
				return false
			}
		},
		Callback: func(fsnotify.Event) { c.restart() },
	}

	// Consume and log any errors from watcher
	go func() {
		for {
			err, ok := <-watcher.Errors
			if !ok {
				return
			}
			log.WithError(err).Error("error while watching drop-ins")
		}
	}()

	log.Infof("started to watch events on %s", c.importsPath)

	err = debouncer.Run(ctx)
	if err != nil {
		log.WithError(err).Warn("dropin watch bouncer exited with error")
	}
}

func (c *ContainerD) restart() {
	log := logrus.WithFields(logrus.Fields{"component": "containerd", "phase": "restart"})

	log.Info("restart requested")
	if err := c.setupConfig(); err != nil {
		log.WithError(err).Warn("failed to resolve config")
		return
	}
	if runtime.GOOS == "windows" {

		if err := c.windowsStop(); err != nil {
			log.WithError(err).Warn("failed to stop windows service")
			return
		}
		if err := c.windowsStart(context.Background()); err != nil {
			log.WithError(err).Warn("failed to start windows service")
			return
		}
	} else {
		p := c.supervisor.GetProcess()
		if err := p.Signal(syscall.SIGHUP); err != nil {
			log.WithError(err).Warn("failed to send SIGHUP")
		}

	}
}

// Stop stops containerD
func (c *ContainerD) Stop() error {
	if runtime.GOOS == "windows" {
		return c.windowsStop()
	}
	return c.supervisor.Stop()
}

// This is the md5sum of the default k0s containerd config file before 1.27
const pre1_27ConfigSum = "59039b43303742a5496b13fd57f9beec"

// isK0sManagedConfig checks if the config file is k0s managed
// - If the config file does not exist, it's k0s managed
// - If the config file md5sum matches the pre 1.27 config, it's k0s managed
// - If the config file has the magic marker, it's k0s managed
func isK0sManagedConfig(path string) (bool, error) {
	// If the file does not exist, it's k0s managed (new install)
	if !file.Exists(path) {
		return true, nil
	}
	pre1_27Managed, err := isPre1_27ManagedConfig(path)
	if err != nil {
		return false, err
	}
	if pre1_27Managed {
		return true, nil
	}
	// Check if the config file has the magic marker
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if strings.Contains(scanner.Text(), magicMarker) {
			return true, nil
		}
	}
	return false, nil
}

func isPre1_27ManagedConfig(path string) (bool, error) {
	// Check MD5 sum of the config file
	// If it matches the pre 1.27 config, it's k0s managed
	md5sum := md5.New()
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()

	if _, err := io.Copy(md5sum, f); err != nil {
		return false, err
	}

	sum := md5sum.Sum(nil)

	pre1_27ConfigSumBytes, err := hex.DecodeString(pre1_27ConfigSum)
	if err != nil {
		return false, err
	}

	if bytes.Equal(pre1_27ConfigSumBytes, sum) {
		return true, nil
	}

	return false, nil
}
