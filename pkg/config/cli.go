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

	"github.com/k0sproject/k0s/pkg/constant"

	"github.com/spf13/pflag"
)

var (
	CfgFile string
	K0sVars CfgVars
)

// This struct holds all the CLI options & settings required by the
// different k0s sub-commands
type CLIOptions struct {
	CfgFile string
	K0sVars *CfgVars
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

	return &CLIOptions{CfgFile, k0sVars}, nil
}
