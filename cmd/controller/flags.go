//go:build unix

/*
Copyright 2025 k0s authors

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
	"errors"
	"fmt"
	"math/bits"
	"slices"
	"strings"
	"time"

	"github.com/k0sproject/k0s/pkg/component/manager"
	"github.com/k0sproject/k0s/pkg/constant"
	"github.com/k0sproject/k0s/pkg/k0scloudprovider"
	"github.com/spf13/pflag"
)

// Shared controller cli flags
type Flags struct {
	NoTaints bool

	ClusterComponents               *manager.Manager
	EnableK0sCloudProvider          bool
	K0sCloudProviderPort            int
	K0sCloudProviderUpdateFrequency time.Duration
	NodeComponents                  *manager.Manager
	EnableDynamicConfig             bool
	EnableMetricsScraper            bool
	KubeControllerManagerExtraArgs  string
	InitOnly                        bool

	enableWorker, single bool
	disabledComponents   componentSetFlag
}

func (f *Flags) Mode() Mode {
	switch {
	case f.single:
		return SingleMode
	case f.enableWorker:
		return ControllerPlusWorkerMode
	default:
		return StandaloneMode
	}
}

func (f *Flags) IsComponentDisabled(name string) bool {
	idx, found := slices.BinarySearch(availableComponents[:], name)
	return found && f.disabledComponents.isSet(idx)
}

func (f *Flags) AddToFlagSet(flags *pflag.FlagSet) {
	flags.BoolVar(&f.enableWorker, "enable-worker", false, "enable worker")
	flags.Var(&f.disabledComponents, "disable-components", "disable components (valid items: "+strings.Join(availableComponents[:], ",")+")")
	flags.BoolVar(&f.single, "single", false, "enable single node (implies --enable-worker)")
	flags.BoolVar(&f.NoTaints, "no-taints", false, "disable default taints for controller node")
	flags.BoolVar(&f.EnableK0sCloudProvider, "enable-k0s-cloud-provider", false, "enables the k0s-cloud-provider (default false)")
	flags.DurationVar(&f.K0sCloudProviderUpdateFrequency, "k0s-cloud-provider-update-frequency", 2*time.Minute, "the frequency of k0s-cloud-provider node updates")
	flags.IntVar(&f.K0sCloudProviderPort, "k0s-cloud-provider-port", k0scloudprovider.DefaultBindPort, "the port that k0s-cloud-provider binds on")
	flags.BoolVar(&f.EnableDynamicConfig, "enable-dynamic-config", false, "enable cluster-wide dynamic config based on custom resource")
	flags.BoolVar(&f.EnableMetricsScraper, "enable-metrics-scraper", false, "enable scraping metrics from the controller components (kube-scheduler, kube-controller-manager)")
	flags.StringVar(&f.KubeControllerManagerExtraArgs, "kube-controller-manager-extra-args", "", "extra args for kube-controller-manager")
	flags.BoolVar(&f.InitOnly, "init-only", false, "only initialize controller and exit")
}

// FIXME this is probably really a config, not a flag thing. Or is it not?
var availableComponents = [...]string{
	constant.ApplierManagerComponentName,
	constant.AutopilotComponentName,
	constant.ControlAPIComponentName,
	constant.CoreDNSComponentname,
	constant.CsrApproverComponentName,
	constant.APIEndpointReconcilerComponentName,
	constant.HelmComponentName,
	constant.KonnectivityServerComponentName,
	constant.KubeControllerManagerComponentName,
	constant.KubeProxyComponentName,
	constant.KubeSchedulerComponentName,
	constant.MetricsServerComponentName,
	constant.NetworkProviderComponentName,
	constant.NodeRoleComponentName,
	constant.SystemRBACComponentName,
	constant.WindowsNodeComponentName,
	constant.WorkerConfigComponentName,
}

type componentSetFlag uint64

func (*componentSetFlag) Type() string {
	// Needs to end with Slice, so that command completion works multiple times.
	return "stringSlice"
}

func (f *componentSetFlag) Set(value string) error {
	modified := *f
	components := strings.Split(value, ",")
	for pos, component := range components {
		if idx, found := slices.BinarySearch(availableComponents[:], component); found {
			modified = modified.set(idx)
			continue
		}

		if len(components) > 1 {
			return fmt.Errorf("unknown component at index %d", pos)
		}

		return errors.New("unknown component")
	}

	*f = modified
	return nil
}

func (f *componentSetFlag) String() string {
	if *f == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteByte('[')
	first := true
	for rest := uint64(*f); rest != 0; /* clear lowest bit: */ rest &= rest - 1 {
		if first {
			first = false
		} else {
			b.WriteByte(',')
		}
		b.WriteString(availableComponents[bits.TrailingZeros64(rest)])
	}
	b.WriteByte(']')
	return b.String()
}

func (f componentSetFlag) isSet(bit int) bool {
	return (f & (1 << bit)) != 0
}

func (f componentSetFlag) set(bit int) componentSetFlag {
	return f | (1 << bit)
}
