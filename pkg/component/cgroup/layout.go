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

package cgroup

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/containerd/cgroups/v3/cgroup2"
	systemddbus "github.com/coreos/go-systemd/v22/dbus"
	"github.com/opencontainers/runc/libcontainer/cgroups"
	"github.com/opencontainers/runc/libcontainer/cgroups/systemd"
	"github.com/sirupsen/logrus"
)

type Layout struct {
	nodeCgroup string
	podsCgroup string
}

func (l *Layout) NodeCgroup() string {
	return l.nodeCgroup
}

func (l *Layout) PodsCgroup() string {
	return l.podsCgroup
}

func (l *Layout) Init(ctx context.Context) error {
	log := logrus.WithField("component", "cgroups-setup")

	// FIXME figure out what to do with legacy / hybrid modes.
	if !cgroups.IsCgroup2UnifiedMode() {
		log.Info("Skipping cgroups setup: only unified mode is supported")
		return nil
	}

	if !systemd.IsRunningSystemd() {
		log.Debug("Not using systemd integration, no systemd found")
	} else if selfInfo, err := inspectSystemdUnit(ctx, log, ""); err != nil {
		log.WithError(err).Warn("Skipping systemd integration")
	} else if selfInfo.delegate {
		log.Debugf(
			"Not using systemd integration, cgroup delegation is turned on for %s backed by cgroup %s",
			selfInfo.name, selfInfo.controlGroup,
		)
		return l.cgroupfsSetup(selfInfo.controlGroup)
	} else {
		return l.systemdSetup(ctx, log, selfInfo)
	}

	return l.cgroupfsSetup("")
}

type systemdUnitInfo struct {
	name         string
	slice        string
	controlGroup string
	delegate     bool
}

// Inspects the given unit by retrieving some of its properties. If unitName is
// empty, the unit of this process is inspected.
func inspectSystemdUnit(ctx context.Context, log logrus.FieldLogger, unitName string) (*systemdUnitInfo, error) {
	// FIXME info logging should be debug, or completely removed.

	conn, err := systemddbus.NewWithContext(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	if unitName == "" {
		unitName, err = conn.GetUnitNameByPID(ctx, 0 /* this means "the caller's PID" */)
		if err != nil {
			return nil, err
		}
		log.Info("Unit name: ", unitName)
	}

	props, err := conn.GetAllPropertiesContext(ctx, unitName)
	if err != nil {
		return nil, err
	}

	controlGroup, err := castValue[string](props, "ControlGroup")
	if err != nil {
		return nil, err
	}
	log.Info("ControlGroup: ", controlGroup)

	// This won't be present on slices.
	var slice string
	if _, kind := cutUnitKind(unitName); kind != "slice" {
		slice, err = castValue[string](props, "Slice")
		if err != nil {
			return nil, err
		}
		log.Info("Slice: ", slice)
	}

	delegate, err := castValue[bool](props, "Delegate")
	if err != nil {
		return nil, err
	}
	log.Info("Delegate: ", delegate)

	return &systemdUnitInfo{unitName, slice, controlGroup, delegate}, nil
}

func (l *Layout) systemdSetup(ctx context.Context, log logrus.FieldLogger, rootInfo *systemdUnitInfo) error {
	// FIXME figure out if containerd/cgroups/v3 gets the job done just as well...
	// Note: dbus retries on disconnect, management of systemd props, especially Wants/Slice.

	// Construct a "root slice" name for k0s to place additional scopes by
	// taking the root slice and adding a sub-slice with the root unit's name.
	// Not that the root unit will always be a service or scope, as it's the
	// unit of which this process is part of, and slices cannot contain
	// processes.
	// parentSlice := cmp.Or(rootInfo.slice, "system.slice")
	// rootName := trimUnitKind(rootInfo.name) + ".slice"
	// if !strings.HasPrefix(rootName, "k0s") {
	// 	rootName = "k0s" + rootName
	// }

	// FIXME decide if the following things are hard errors, i.e. will stop k0s.

	// FIXME need to deal with an existing slice here?

	// This should contain the kube pods.
	// rootMgr, err := cgroup2.NewSystemd(parentSlice, rootName, -1, nil)
	// if err != nil {
	// 	return fmt.Errorf("failed to create %s: %w", rootName, err)
	// }

	// podsMgr, err := cgroup2.NewSystemd(rootSlice, "pods.slice", -1, nil)
	// if err != nil {
	// 	return fmt.Errorf("failed to create pods.slice: %w", err)
	// }

	// nodeMgr.NewChild()

	// // No need to deal with systemd here, as this cgroup already exists.
	// rootMgr, err := cgroup2.Load(rootInfo.controlGroup)
	// if err != nil {
	// 	return fmt.Errorf("failed to load cgroup %s: %w", rootInfo.controlGroup, err)
	// }

	// pids, err := rootMgr.Procs(false)

	// pids, err = rootMgr.GetPids()
	// if err != nil {
	// 	return err
	// }
	// if len(pids) != 1 {
	// 	// FIXME Maybe use the parent slice then?
	// 	return fmt.Errorf("expected a single PID in %s: %v", rootInfo.name, pids)
	// }

	// nodeMgr, err := systemd.NewUnifiedManager(&configs.Cgroup{
	// 	ScopePrefix: "k0s",
	// 	Name:        "node",
	// 	Parent:      rootInfo.name,
	// 	Systemd:     true,
	// }, "")

	// cgroupmanager.New(&configs.Cgroup{
	// 	Systemd: true,
	// })
	// if err != nil {
	// 	return fmt.Errorf("failed to construct k0snode.scope beneath %s: %w", rootUnit, err)
	// }

	// FIXME what if this cgroup/scope already exists? Can we reuse it?

	// if err := nodeMgr.Apply(pids[0]); err != nil {
	// 	return fmt.Errorf("failed to move PID %d into k0snode.scope: %w", pids[0], err)
	// }

	// nodeInfo, err := inspectSystemdUnit(ctx, log, "k0snode.scope")
	// if err != nil {
	// 	return fmt.Errorf("failed to inspect k0snode.scope: %w", err)
	// }

	// log.Info("Moved into k0snode.scope: %+v", nodeInfo)
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

func (l *Layout) cgroupfsSetup(controlGroup string) error {
	if controlGroup == "" {
		var err error
		controlGroup, err = cgroup2.NestedGroupPath("")
		if err != nil {
			return err
		}
	}

	rootMgr, err := cgroup2.Load(controlGroup)
	if err != nil {
		return fmt.Errorf("failed to load cgroup %s: %w", controlGroup, err)
	}

	pids, err := rootMgr.Procs(false)
	if err != nil {
		return fmt.Errorf("while loading cgroup procs for %s: %w", controlGroup, err)
	}
	if len(pids) != 1 {
		return fmt.Errorf("expected cgroup %s to contain exactly one process: %v", controlGroup, pids)
	}

	nodeMgr, err := rootMgr.NewChild("k0snode", nil)
	if err != nil {
		return fmt.Errorf("failed to create k0snode cgroup: %w", err)
	}

	if err := rootMgr.MoveTo(nodeMgr); err != nil {
		return fmt.Errorf("failed to move into node cgroup: %w", err)
	}

	if _, err = rootMgr.NewChild("k0spods", nil); err != nil {
		return fmt.Errorf("failed to create k0spods cgroup: %w", err)
	}

	l.nodeCgroup = filepath.Join(controlGroup, "k0snode")
	l.podsCgroup = filepath.Join(controlGroup, "k0spods")
	return nil
}

func (l *Layout) Start(context.Context) error { return nil }
func (l *Layout) Stop() error                 { return nil }

func castValue[V any, K comparable](props map[K]any, key K) (v V, _ error) {
	raw, ok := props[key]
	if !ok {
		return v, fmt.Errorf("no such key: %v", key)
	}
	v, ok = raw.(V)
	if !ok {
		return v, fmt.Errorf("expected %v to be %T, got %T: %v", key, v, raw, raw)
	}
	return v, nil
}

func trimUnitKind(unit string) string {
	name, _ := cutUnitKind(unit)
	return name
}

func cutUnitKind(unit string) (name, kind string) {
	idx := strings.LastIndexByte(unit, '.')
	if idx > 0 {
		kind := unit[idx+1:]
		switch kind {
		case "service", "scope", "slice":
			return unit[:idx], kind
		}
	}

	return unit, ""
}
