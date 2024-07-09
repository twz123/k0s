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

package controller

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/k0sproject/k0s/internal/pkg/dir"
	"github.com/k0sproject/k0s/internal/pkg/stringmap"
	"github.com/k0sproject/k0s/internal/pkg/users"
	"github.com/k0sproject/k0s/pkg/apis/k0s/v1beta1"
	"github.com/k0sproject/k0s/pkg/assets"
	"github.com/k0sproject/k0s/pkg/component/manager"
	"github.com/k0sproject/k0s/pkg/component/prober"
	"github.com/k0sproject/k0s/pkg/config"
	"github.com/k0sproject/k0s/pkg/constant"
	"github.com/k0sproject/k0s/pkg/k0scontext"
	"github.com/k0sproject/k0s/pkg/supervisor"
)

// Konnectivity implements the component interface for konnectivity server
type Konnectivity struct {
	K0sVars     *config.CfgVars
	LogLevel    string
	ServerCount func() (uint, <-chan struct{})

	supervisor *supervisor.Supervisor
	uid        int

	stopFunc      context.CancelFunc
	clusterConfig *v1beta1.ClusterConfig
	log           *logrus.Entry

	*prober.EventEmitter
}

var _ manager.Component = (*Konnectivity)(nil)

// Init ...
func (k *Konnectivity) Init(ctx context.Context) error {
	var err error
	k.uid, err = users.GetUID(constant.KonnectivityServerUser)
	if err != nil {
		k.EmitWithPayload("error getting UID for", err)
		logrus.Warn("running konnectivity as root: ", err)
	}
	err = dir.Init(k.K0sVars.KonnectivitySocketDir, 0755)
	if err != nil {
		k.EmitWithPayload("failed to initialize socket directory", err)
		return fmt.Errorf("failed to initialize directory %s: %w", k.K0sVars.KonnectivitySocketDir, err)
	}

	err = os.Chown(k.K0sVars.KonnectivitySocketDir, k.uid, -1)
	if err != nil && os.Geteuid() == 0 {
		k.EmitWithPayload("failed to chown socket directory", err)
		return fmt.Errorf("failed to chown %s: %w", k.K0sVars.KonnectivitySocketDir, err)
	}

	k.log = logrus.WithFields(logrus.Fields{"component": "konnectivity"})
	if err := assets.Stage(k.K0sVars.BinDir, "konnectivity-server", constant.BinDirMode); err != nil {
		k.EmitWithPayload("failed to stage konnectivity-server", err)
		return fmt.Errorf("failed to stage konnectivity-server binary %w", err)

	}
	defer k.Emit("successfully initialized konnectivity component")

	k.clusterConfig = k0scontext.GetNodeConfig(ctx)

	return nil
}

// Run ..
func (k *Konnectivity) Start(ctx context.Context) error {
	serverCount, serverCountChanged := k.ServerCount()

	if err := k.runServer(serverCount); err != nil {
		k.EmitWithPayload("failed to start konnectivity server", err)
		return fmt.Errorf("failed to start konnectivity server: %w", err)
	}

	go func() {
		var retry <-chan time.Time
		for {
			select {
			case <-serverCountChanged:
				prevServerCount := serverCount
				serverCount, serverCountChanged = k.ServerCount()
				// restart only if the server count actually changed
				if serverCount == prevServerCount {
					continue
				}

			case <-retry:
				k.Emit("retrying to start konnectivity server")
				k.log.Info("Retrying to start konnectivity server")

			case <-ctx.Done():
				k.Emit("stopped konnectivity server")
				k.log.Info("stopping konnectivity server reconfig loop")
				return
			}

			retry = nil

			if err := k.runServer(serverCount); err != nil {
				k.EmitWithPayload("failed to start konnectivity server", err)
				k.log.WithError(err).Errorf("Failed to start konnectivity server")
				retry = time.After(10 * time.Second)
				continue
			}
		}
	}()

	return nil
}

func (k *Konnectivity) serverArgs(count uint) []string {
	return stringmap.StringMap{
		"--uds-name":                 filepath.Join(k.K0sVars.KonnectivitySocketDir, "konnectivity-server.sock"),
		"--cluster-cert":             filepath.Join(k.K0sVars.CertRootDir, "server.crt"),
		"--cluster-key":              filepath.Join(k.K0sVars.CertRootDir, "server.key"),
		"--kubeconfig":               k.K0sVars.KonnectivityKubeConfigPath,
		"--mode":                     "grpc",
		"--server-port":              "0",
		"--agent-port":               fmt.Sprintf("%d", k.clusterConfig.Spec.Konnectivity.AgentPort),
		"--admin-port":               fmt.Sprintf("%d", k.clusterConfig.Spec.Konnectivity.AdminPort),
		"--agent-namespace":          "kube-system",
		"--agent-service-account":    "konnectivity-agent",
		"--authentication-audience":  "system:konnectivity-server",
		"--logtostderr":              "true",
		"--stderrthreshold":          "1",
		"--v":                        k.LogLevel,
		"--enable-profiling":         "false",
		"--delete-existing-uds-file": "true",
		"--server-count":             strconv.FormatUint(uint64(count), 10),
		"--server-id":                k.K0sVars.InvocationID,
		"--proxy-strategies":         "destHost,default",
		"--cipher-suites":            constant.AllowedTLS12CipherSuiteNames(),
	}.ToArgs()
}

func (k *Konnectivity) runServer(count uint) error {
	// Stop supervisor
	if k.supervisor != nil {
		k.EmitWithPayload("restarting konnectivity server due to server count change",
			map[string]interface{}{"serverCount": count})
		if err := k.supervisor.Stop(); err != nil {
			k.log.Errorf("failed to stop supervisor: %s", err)
		}
	}

	k.supervisor = &supervisor.Supervisor{
		Name:    "konnectivity",
		BinPath: assets.BinPath("konnectivity-server", k.K0sVars.BinDir),
		DataDir: k.K0sVars.DataDir,
		RunDir:  k.K0sVars.RunDir,
		Args:    k.serverArgs(count),
		UID:     k.uid,
	}
	err := k.supervisor.Supervise()
	if err != nil {
		k.supervisor = nil // not to make the next loop to try to stop it first
		return err
	}
	k.EmitWithPayload("started konnectivity server", map[string]interface{}{"serverCount": count})

	return nil
}

// Stop stops
func (k *Konnectivity) Stop() error {
	if k.stopFunc != nil {
		logrus.Debug("closing konnectivity component context")
		k.stopFunc()
	}
	if k.supervisor == nil {
		return nil
	}
	logrus.Debug("about to stop konnectivity supervisor")
	return k.supervisor.Stop()
}

func (k *Konnectivity) Healthy() error {
	if k.clusterConfig == nil {
		return fmt.Errorf("cluster config not yet available")
	}

	return nil
}
