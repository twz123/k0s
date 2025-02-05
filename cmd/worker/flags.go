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

package worker

import (
	"fmt"
	"strings"

	"github.com/k0sproject/k0s/pkg/config"
	"github.com/spf13/pflag"
)

// Shared worker cli flags
type Flags struct {
	CloudProvider    bool
	LogLevels        config.LogLevels
	CRISocket        string
	KubeletExtraArgs string
	Labels           []string
	Taints           []string
	TokenFile        string
	TokenArg         string
	WorkerProfile    string
	IPTablesMode     string
}

func (f *Flags) AddToFlagSet(flags *pflag.FlagSet) {
	// FIXME this is wonky... Maybe add a Default method to the Flags struct
	// Or make the zero value the default value somehow.
	f.LogLevels = config.DefaultLogLevels()

	var deprecatedFlags pflag.FlagSet
	deprecatedFlags.String("cidr-range", "", "")
	deprecatedFlags.VisitAll(func(f *pflag.Flag) {
		f.Hidden = true
		f.Deprecated = "it has no effect and will be removed in a future release"
		flags.AddFlag(f)
	})

	flags.String("kubelet-root-dir", "", "Kubelet root directory for k0s")
	flags.StringVar(&f.WorkerProfile, "profile", "default", "worker profile to use on the node")
	flags.BoolVar(&f.CloudProvider, "enable-cloud-provider", false, "Whether or not to enable cloud provider support in kubelet")
	flags.StringVar(&f.TokenFile, "token-file", "", "Path to the file containing join-token.")
	flags.VarP((*logLevelsFlag)(&f.LogLevels), "logging", "l", "Logging Levels for the different components")
	flags.StringSliceVarP(&f.Labels, "labels", "", []string{}, "Node labels, list of key=value pairs")
	flags.StringSliceVarP(&f.Taints, "taints", "", []string{}, "Node taints, list of key=value:effect strings")
	flags.StringVar(&f.KubeletExtraArgs, "kubelet-extra-args", "", "extra args for kubelet")
	flags.StringVar(&f.IPTablesMode, "iptables-mode", "", "iptables mode (valid values: nft, legacy, auto). default: auto")
	flags.AddFlagSet(GetCRISocketFlag(&f.CRISocket))
}

func GetCRISocketFlag(value *string) *pflag.FlagSet {
	flagset := &pflag.FlagSet{}
	flagset.StringVar(value, "cri-socket", "", "container runtime socket to use, default to internal containerd. Format: [remote|docker]:[path-to-socket]")
	return flagset
}

type logLevelsFlag config.LogLevels

func (f *logLevelsFlag) Type() string {
	return "stringToString"
}

func (f *logLevelsFlag) Set(val string) error {
	val = strings.TrimPrefix(val, "[")
	val = strings.TrimSuffix(val, "]")

	parsed := config.DefaultLogLevels()

	for val != "" {
		pair, rest, _ := strings.Cut(val, ",")
		val = rest
		k, v, ok := strings.Cut(pair, "=")

		if k == "" {
			return fmt.Errorf("component name cannot be empty: %q", pair)
		}
		if !ok {
			return fmt.Errorf("must be of format component=level: %q", pair)
		}

		switch k {
		case "containerd":
			parsed.Containerd = v
		case "etcd":
			parsed.Etcd = v
		case "konnectivity-server":
			parsed.Konnectivity = v
		case "kube-apiserver":
			parsed.KubeAPIServer = v
		case "kube-controller-manager":
			parsed.KubeControllerManager = v
		case "kube-scheduler":
			parsed.KubeScheduler = v
		case "kubelet":
			parsed.Kubelet = v
		default:
			return fmt.Errorf("unknown component name: %q", k)
		}
	}

	*f = parsed
	return nil
}

func (f *logLevelsFlag) String() string {
	l := (*config.LogLevels)(f)
	var buf strings.Builder
	buf.WriteString("[containerd=")
	buf.WriteString(l.Containerd)
	buf.WriteString(",etcd=")
	buf.WriteString(l.Etcd)
	buf.WriteString(",konnectivity-server=")
	buf.WriteString(l.Konnectivity)
	buf.WriteString(",kube-apiserver=")
	buf.WriteString(l.KubeAPIServer)
	buf.WriteString(",kube-controller-manager=")
	buf.WriteString(l.KubeControllerManager)
	buf.WriteString(",kube-scheduler=")
	buf.WriteString(l.KubeScheduler)
	buf.WriteString(",kubelet=")
	buf.WriteString(l.Kubelet)
	buf.WriteString("]")
	return buf.String()
}
