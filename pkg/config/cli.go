/*
Copyright 2021 k0s authors

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

package config

import (
	"fmt"
	"strings"

	"github.com/k0sproject/k0s/pkg/constant"

	"github.com/spf13/pflag"
)

var (
	CfgFile    string
	K0sVars    CfgVars
	workerOpts WorkerOptions
)

// This struct holds all the CLI options & settings required by the
// different k0s sub-commands
type CLIOptions struct {
	WorkerOptions
	CfgFile string
	K0sVars *CfgVars
}

// Shared worker cli flags
type WorkerOptions struct {
	CloudProvider    bool
	LogLevels        LogLevels
	CriSocket        string
	KubeletExtraArgs string
	Labels           []string
	Taints           []string
	TokenFile        string
	TokenArg         string
	WorkerProfile    string
	IPTablesMode     string
}

type LogLevels = struct {
	Containerd            string
	Etcd                  string
	Konnectivity          string
	KubeAPIServer         string
	KubeControllerManager string
	KubeScheduler         string
	Kubelet               string
}

func DefaultLogLevels() LogLevels {
	return LogLevels{
		Containerd:            "info",
		Etcd:                  "info",
		Konnectivity:          "1",
		KubeAPIServer:         "1",
		KubeControllerManager: "1",
		KubeScheduler:         "1",
		Kubelet:               "1",
	}
}

type logLevelsFlag LogLevels

func (f *logLevelsFlag) Type() string {
	return "stringToString"
}

func (f *logLevelsFlag) Set(val string) error {
	val = strings.TrimPrefix(val, "[")
	val = strings.TrimSuffix(val, "]")

	parsed := DefaultLogLevels()

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
	var buf strings.Builder
	buf.WriteString("[containerd=")
	buf.WriteString(f.Containerd)
	buf.WriteString(",etcd=")
	buf.WriteString(f.Etcd)
	buf.WriteString(",konnectivity-server=")
	buf.WriteString(f.Konnectivity)
	buf.WriteString(",kube-apiserver=")
	buf.WriteString(f.KubeAPIServer)
	buf.WriteString(",kube-controller-manager=")
	buf.WriteString(f.KubeControllerManager)
	buf.WriteString(",kube-scheduler=")
	buf.WriteString(f.KubeScheduler)
	buf.WriteString(",kubelet=")
	buf.WriteString(f.Kubelet)
	buf.WriteString("]")
	return buf.String()
}

func GetPersistentFlagSet() *pflag.FlagSet {
	flagset := &pflag.FlagSet{}
	flagset.String("data-dir", constant.DataDirDefault, "Data Directory for k0s. DO NOT CHANGE for an existing setup, things will break!")
	flagset.String("status-socket", "", "Full file path to the socket file. (default: <rundir>/status.sock)")
	return flagset
}

func GetKubeCtlFlagSet() *pflag.FlagSet {
	flagset := &pflag.FlagSet{}
	flagset.String("data-dir", constant.DataDirDefault, "Data Directory for k0s. DO NOT CHANGE for an existing setup, things will break!")
	return flagset
}

func GetCriSocketFlag() *pflag.FlagSet {
	flagset := &pflag.FlagSet{}
	flagset.StringVar(&workerOpts.CriSocket, "cri-socket", "", "container runtime socket to use, default to internal containerd. Format: [remote|docker]:[path-to-socket]")
	return flagset
}

func GetWorkerFlags() *pflag.FlagSet {
	flagset := &pflag.FlagSet{}

	if workerOpts.LogLevels == (LogLevels{}) {
		// initialize zero value with defaults
		workerOpts.LogLevels = DefaultLogLevels()
	}

	flagset.String("cidr-range", "", "")
	flagset.VisitAll(func(f *pflag.Flag) {
		f.Hidden = true
		f.Deprecated = "it has no effect and will be removed in a future release"
	})

	flagset.String("kubelet-root-dir", "", "Kubelet root directory for k0s")
	flagset.StringVar(&workerOpts.WorkerProfile, "profile", "default", "worker profile to use on the node")
	flagset.BoolVar(&workerOpts.CloudProvider, "enable-cloud-provider", false, "Whether or not to enable cloud provider support in kubelet")
	flagset.StringVar(&workerOpts.TokenFile, "token-file", "", "Path to the file containing join-token.")
	flagset.VarP((*logLevelsFlag)(&workerOpts.LogLevels), "logging", "l", "Logging Levels for the different components")
	flagset.StringSliceVarP(&workerOpts.Labels, "labels", "", []string{}, "Node labels, list of key=value pairs")
	flagset.StringSliceVarP(&workerOpts.Taints, "taints", "", []string{}, "Node taints, list of key=value:effect strings")
	flagset.StringVar(&workerOpts.KubeletExtraArgs, "kubelet-extra-args", "", "extra args for kubelet")
	flagset.StringVar(&workerOpts.IPTablesMode, "iptables-mode", "", "iptables mode (valid values: nft, legacy, auto). default: auto")
	flagset.AddFlagSet(GetCriSocketFlag())

	return flagset
}

// The config flag used to be a persistent, joint flag to all commands
// now only a few commands use it. This function helps to share the flag with multiple commands without needing to define
// it in multiple places
func FileInputFlag() *pflag.FlagSet {
	flagset := &pflag.FlagSet{}
	descString := fmt.Sprintf("config file, use '-' to read the config from stdin (default %q)", constant.K0sConfigPathDefault)
	flagset.StringVarP(&CfgFile, "config", "c", "", descString)

	return flagset
}

func GetCmdOpts(cobraCmd command) (*CLIOptions, error) {
	k0sVars, err := NewCfgVars(cobraCmd)
	if err != nil {
		return nil, err
	}

	// if a runtime config can be loaded, use it to override the k0sVars
	if rtc, err := LoadRuntimeConfig(k0sVars.RuntimeConfigPath); err == nil {
		k0sVars = rtc.K0sVars
	}

	return &CLIOptions{
		WorkerOptions: workerOpts,

		CfgFile: CfgFile,
		K0sVars: k0sVars,
	}, nil
}
