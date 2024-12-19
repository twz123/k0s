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
	"errors"

	"github.com/k0sproject/k0s/internal/pkg/sysinfo/probes"
)

type InitSystemProbes struct {
	path   probes.ProbePath
	probes []initSystemProbe
}

type initSystemProbe interface {
	detect() (probes.ProbedProp, error)
	probes.Probe
}

func (l *LinuxProbes) AssertInitSystem() *InitSystemProbes {
	var i *InitSystemProbes
	l.Set("initSystem", func(path probes.ProbePath, current probes.Probe) probes.Probe {
		if probe, ok := current.(*InitSystemProbes); ok {
			i = probe
			return i
		}

		i = &InitSystemProbes{path, nil}
		return i
	})

	return i
}

func (i *InitSystemProbes) Probe(reporter probes.Reporter) error {
	desc := probes.NewProbeDesc("Init system", i.path)
	var errs []error
	for _, initSys := range i.probes {
		if prop, err := initSys.detect(); err != nil {
			errs = append(errs, err)
			continue
		} else if prop != nil {
			if err := reporter.Pass(desc, prop); err != nil {
				return err
			}
			return initSys.Probe(reporter)
		}
	}

	if errs != nil {
		return reporter.Error(desc, probes.ErrorProp(errors.Join(errs...)))
	}

	return reporter.Pass(desc, probes.StringProp("other"))
}
