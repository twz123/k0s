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

package internal

import (
	"errors"
	"net/http"
	_ "net/http/pprof"
	"os"
	"strconv"

	internallog "github.com/k0sproject/k0s/internal/pkg/log"

	"github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type DebugFlags struct {
	logsToStdout     bool
	verbose          bool
	verboseByDefault bool
	debug            bool
	debugListenOn    string
}

func (f *DebugFlags) IsDebug() bool {
	return f.debug
}

// Configures the debug flags for long-running commands.
// Must be called before adding the flags to a flag set.
func (f *DebugFlags) LongRunning() *DebugFlags {
	f.logsToStdout = true

	// The default value won't be reflected in the flag set
	// once the flags have been added.
	f.verboseByDefault = true

	return f
}

// Adds the debug flags to the given FlagSet.
func (f *DebugFlags) AddToFlagSet(flags *pflag.FlagSet) {
	flags.BoolVarP(&f.verbose, "verbose", "v", f.verboseByDefault, "Verbose logging")
	flags.BoolVarP(&f.debug, "debug", "d", false, "Debug logging (implies verbose logging)")
	flags.StringVar(&f.debugListenOn, "debugListenOn", ":6060", "Http listenOn for Debug pprof handler")
}

// Adds the debug flags to the given FlagSet when in "kubectl" mode.
// This won't use shorthands, as this will interfere with kubectl's flags.
func (f *DebugFlags) AddToKubectlFlagSet(flags *pflag.FlagSet) {
	debugDefault := false
	if v, ok := os.LookupEnv("DEBUG"); ok {
		debugDefault, _ = strconv.ParseBool(v)
	}

	flags.BoolVar(&f.debug, "debug", debugDefault, "Debug logging [$DEBUG]")
}

func (f *DebugFlags) Run(cmd *cobra.Command, _ []string) {
	if f.logsToStdout {
		logrus.SetOutput(cmd.OutOrStdout())
	}

	switch {
	case f.debug:
		internallog.SetDebugLevel()

		if f.verbose {
			if !f.verboseByDefault {
				logrus.Debug("--debug already implies --verbose")
			}
		} else if f.verboseByDefault {
			logrus.Debug("--debug overrides --verbose=false")
		}

		go func() {
			log := logrus.WithField("debug_server", f.debugListenOn)
			log.Debug("Starting debug server")
			if err := http.ListenAndServe(f.debugListenOn, nil); !errors.Is(err, http.ErrServerClosed) {
				log.WithError(err).Debug("Failed to start debug server")
			} else {
				log.Debug("Debug server closed")
			}
		}()

	case f.verbose:
		internallog.SetInfoLevel()
	}
}
