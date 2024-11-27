//go:build linux

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

package linux

import (
	"context"
	"fmt"
	"os"
	"slices"
	"time"

	"github.com/coreos/go-systemd/v22/dbus"
	"github.com/k0sproject/k0s/internal/pkg/sysinfo/probes"
	"github.com/opencontainers/runc/libcontainer/cgroups/systemd"
)

type SystemdProbes struct {
	probes.Probes
}

func (i *InitSystemProbes) AssertSystemd() *SystemdProbes {
	for _, initSys := range i.probes {
		if s, ok := initSys.(*SystemdProbes); ok {
			return s
		}
	}

	s := &SystemdProbes{probes.NewProbesAtPath(i.path)}
	i.probes = append(i.probes, s)
	return s
}

// detect implements initSystemProbe.
func (s *SystemdProbes) detect() (probes.ProbedProp, error) {
	if systemd.IsRunningSystemd() {
		return probes.StringProp("systemd"), nil
	}

	return nil, nil
}

func (s *SystemdProbes) AssertVersion() {
	s.Set("systemdVersion", func(path probes.ProbePath, _ probes.Probe) probes.Probe {
		return probes.ProbeFn(func(r probes.Reporter) error {
			desc := probes.NewProbeDesc("systemd version", path)

			return withSystemd(func(ctx context.Context, conn *dbus.Conn) error {
				version, err := conn.Version(ctx)
				if err != nil {
					return r.Warn(desc, probes.ErrorProp(err), "")
				}

				// FIXME inspect version and add warnings according to runc:
				// https://github.com/opencontainers/runc/blob/main/docs/cgroup-v2.md#systemd
				return r.Pass(desc, probes.StringProp(version))
			})
		})
	})
}

func (s *SystemdProbes) AssertSystemState(expected string) {
	s.Set("systemdSystemState", func(path probes.ProbePath, _ probes.Probe) probes.Probe {
		return probes.ProbeFn(func(r probes.Reporter) error {
			desc := probes.NewProbeDesc("systemd System State", path)
			return withSystemd(func(ctx context.Context, conn *dbus.Conn) error {
				sysState, err := conn.SystemStateContext(ctx)
				if err != nil {
					return r.Warn(desc, probes.ErrorProp(err), "")
				}
				raw := sysState.Value.Value()
				value, ok := raw.(string)
				if !ok {
					return r.Error(desc, fmt.Errorf("expected a string, got %T: %v", raw, raw))
				}
				if value != expected {
					return r.Warn(desc, probes.StringProp(value), "expected "+expected)
				}

				return r.Pass(desc, probes.StringProp(value))
			})
		})
	})
}

func (s *SystemdProbes) AssertUnit() {
	s.Set("systemdUnit", func(path probes.ProbePath, _ probes.Probe) probes.Probe {
		return probes.ProbeFn(func(r probes.Reporter) error {
			return withSystemd(func(ctx context.Context, conn *dbus.Conn) error {
				desc := probes.NewProbeDesc("systemd Unit", path)

				unitName, err := conn.GetUnitNameByPID(ctx, uint32(os.Getpid()))
				if err != nil {
					return r.Warn(desc, probes.ErrorProp(err), "")
				}
				if err := r.Pass(desc, probes.StringProp(unitName)); err != nil {
					return err
				}

				return probeCgroupDelegation(ctx, conn, unitName, path, r)
			})
		})
	})
}

func probeCgroupDelegation(ctx context.Context, conn *dbus.Conn, unitName string, path probes.ProbePath, reporter probes.Reporter) error {
	path = append(slices.Clone(path), "cgroupDelegation")
	desc := probes.NewProbeDesc("cgroup delegation", path)

	props, err := conn.GetAllPropertiesContext(ctx, unitName)
	if err != nil {
		return reporter.Error(desc, err)
	}

	delegateProp, ok := props["Delegate"]
	if !ok {
		return reporter.Warn(desc, (*systemdDelegateProp)(nil), "")
	}

	delegate, ok := delegateProp.(bool)
	if !ok {
		return reporter.Error(desc, fmt.Errorf("expected Delegate to be bool, got %T: %v", delegateProp, delegateProp))
	}

	return reporter.Pass(desc, (*systemdDelegateProp)(&delegate))
}

func withSystemd(f func(context.Context, *dbus.Conn) error) error {
	ctx, cancel := context.WithTimeout(context.TODO(), 3*time.Second)
	defer cancel()

	conn, err := dbus.NewSystemConnectionContext(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	return f(ctx, conn)
}

type systemdDelegateProp bool

func (d *systemdDelegateProp) String() string {
	if d == nil {
		return "unknown"
	}
	if *d {
		return "enabled"
	}
	return "disabled"
}
