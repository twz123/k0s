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

package cgroups

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"

	"github.com/k0sproject/k0s/pkg/component/manager"

	"github.com/containerd/cgroups/v3/cgroup2"
	systemddbus "github.com/coreos/go-systemd/v22/dbus"
	"github.com/opencontainers/runc/libcontainer/cgroups"
	cgroupmanager "github.com/opencontainers/runc/libcontainer/cgroups/manager"
	"github.com/opencontainers/runc/libcontainer/cgroups/systemd"
	"github.com/opencontainers/runc/libcontainer/configs"
	"github.com/sirupsen/logrus"
)

type Setup struct {
	root *Path
	k0s  *Path
}

var _ manager.Component = (*Setup)(nil)

func (s *Setup) Init(ctx context.Context) error {
	log := logrus.WithField("component", "cgroups-setup")

	// FIXME figure out what to do with legacy / hybrid modes.
	if !cgroups.IsCgroup2UnifiedMode() {
		log.Info("Skipping cgroups setup: only unified mode is supported")
		return nil
	}

	// lccgroups.GetOwnCgroupPath()

	var (
		pid        uint32
		cgroupPath string
	)
	if osPid := os.Getpid(); osPid > 0 && osPid <= math.MaxUint32 {
		pid = uint32(osPid)
		var err error
		cgroupPath, err = cgroup2.PidGroupPath(osPid)
		log.Debug("PidGroupPath: ", cgroupPath, " ", err)

	} else {
		return fmt.Errorf("invalid PID: %d", osPid)
	}

	if systemd.IsRunningSystemd() {
		conn, err := systemddbus.NewWithContext(ctx)

		if err != nil {
			log.WithError(err).Info("Not using systemd integration")
			return err // FIXME
		}
		defer conn.Close()

		sysState, err := conn.SystemStateContext(ctx)
		if err != nil {
			return err // FIXME
		}
		log.Info(sysState)

		unitName, err := conn.GetUnitNameByPID(ctx, pid)
		if err != nil {
			return err // FIXME
		}
		log.Info("Unit name (PID): ", unitName)

		unitName, err = conn.GetUnitNameByControlGroup(ctx, cgroupPath)
		if err != nil {
			return err // FIXME
		}
		log.Info("Unit name (cgroup path): ", unitName)

		props, err := conn.GetAllPropertiesContext(ctx, unitName)
		if err != nil {
			return err // FIXME
		}

		sliceProp, ok := props["Slice"]
		if !ok {
			// FIXME
			return fmt.Errorf("Unit has no Slice: %s", unitName)
		}
		sliceName, ok := sliceProp.(string)
		if !ok {
			// FIXME
			return fmt.Errorf("expected Slice to be string, got %T: %v", sliceProp, sliceProp)
		}
		log.Info("Slice name: ", sliceName)

		var delegate bool
		delegateProp, ok := props["Delegate"]
		if ok {
			delegate, ok = delegateProp.(bool)
			if !ok {
				// FIXME
				return fmt.Errorf("expected Delegate to be bool, got %T: %v", delegateProp, delegateProp)
			}
			log.Info("cgroup delegation: ", delegate)
		}

		// FIXME maybe use similar logic as in systemd's cg_path_get_user_slice.
		// That seems to be used by GNOME VTE to find the cgroup parent.

		// FIXME Figure out how tmux places its cgroups

		if false && !delegate {
			var path string

			cgroupmanager.New(&configs.Cgroup{
				Name:         unitName,
				Parent:       "",
				Path:         cgroupPath,
				ScopePrefix:  "",
				Resources:    &configs.Resources{},
				Systemd:      true,
				SystemdProps: []systemddbus.Property{},
			})

			_ /*mgr*/, err := systemd.NewUnifiedManager(&configs.Cgroup{
				Name:         unitName,
				Parent:       "",
				Path:         cgroupPath,
				ScopePrefix:  "",
				Resources:    &configs.Resources{},
				Systemd:      true,
				SystemdProps: []systemddbus.Property{},
			}, path /* ??? */)
			if err != nil {
				return err
			}
		}

	} else {
		return errors.New("FIXME no systemd")
	}

	// rootGroup, err := cgroup2.Load(rootPath)
	// if err != nil {
	// 	return err
	// }

	// rootGroup.
	// 	s.root = &Path{rootPath, rootGroup}

	// if pids, err := rootGroup.Procs(false); err != nil {
	// 	return err
	// } else if len(pids) != 1 || pids[0] != uint64(pid) {
	// 	pids = slices.DeleteFunc(pids, func(p uint64) bool { return p == pid })
	// 	log.Infof("Skipping cgroups setup: k0s is not the only process in cgroup %s: %s", rootPath, pids)
	// }

	// k0sCgroup, err := rootGroup.NewChild("k0s.scope", nil)
	// if err != nil {
	// 	return err
	// }

	// if err := rootGroup.MoveTo(k0sCgroup); err != nil {
	// 	return fmt.Errorf("failed to move k0s into cgroup %s/k0s.scope: %w", rootPath, err)
	// }

	// if _, exists := extras["--runtime-cgroups"]; !exists {
	// 	switch cgroups.Mode() {
	// 	case cgroups.Unified | cgroups.Hybrid:
	// 		k0sCgroup, err := cgroup2.NestedGroupPath("")
	// 		if err != nil {
	// 			logrus.WithError(err).Info("Won't add --runtime-cgroups flag to kubelet arguments")
	// 		} else {
	// 			args["--runtime-cgroups"] = k0sCgroup
	// 		}
	// 	default:
	// 		logrus.Debug("Won't add --runtime-cgroups flag to kubelet arguments")
	// 	}
	// }

	return nil
}

func (s *Setup) Start(context.Context) error { return nil }

func (s *Setup) Stop() error {
	if s.root == nil {
		return nil
	}

	if s.k0s != nil {
		// FIXME hmpf!
		if err := s.k0s.mgr.(*cgroup2.Manager).MoveTo(s.root.mgr.(*cgroup2.Manager)); err != nil {
			return err
		}
		return s.k0s.mgr.Delete()
	}

	return nil
}
