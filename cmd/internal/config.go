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

package internal

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sync/atomic"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type ConfigFlag struct {
	value    string
	required bool
	stdin    *configStdin
}

type configStdin struct {
	reader  func() io.Reader
	grabbed atomic.Bool
}

func (f *ConfigFlag) WithStdin(stdin func() io.Reader) *ConfigFlag {
	f.stdin = &configStdin{reader: stdin}
	return f
}

func (f *ConfigFlag) Loader() func() ([]byte, error) {
	switch f.value {
	case "":
		return nil

	case "-":
		stdin := f.stdin
		if stdin != nil {
			return func() ([]byte, error) {
				if stdin.grabbed.Swap(true) {
					return nil, fmt.Errorf("standard input already grabbed")
				}
				return io.ReadAll(stdin.reader())
			}
		}
		fallthrough

	default:
		path := f.value
		return func() ([]byte, error) {
			return os.ReadFile(path)
		}
	}
}

func (f *ConfigFlag) Required() *ConfigFlag {
	f.required = true
	return f
}

func (f *ConfigFlag) AddToFlagSet(flags *pflag.FlagSet) {
	flag := &pflag.Flag{
		Name:      "config",
		Shorthand: "c",
		Usage:     "path to configuration file",
		Value:     (*configFlagValue)(f),
	}

	if f.stdin != nil {
		flag.Usage += ", or '-' to read configuration from standard input"
	}

	flags.AddFlag(flag)
	if f.required {
		cobra.MarkFlagRequired(flags, flag.Name)
	}
}

type configFlagValue ConfigFlag

func (*configFlagValue) Type() string     { return "string" }
func (v *configFlagValue) String() string { return v.value }

func (v *configFlagValue) Set(value string) error {
	if value == "" && v.required {
		return errors.New("may not be empty")
	}

	v.value = value
	return nil
}
