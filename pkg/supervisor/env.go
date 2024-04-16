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

package supervisor

import (
	"fmt"
	"os"
	"strings"
)

type envSpec struct {
	componentPrefix string
	pathPrefix      string
	keepEnvPrefix   bool
}

// Prepares a component's process environment for execution by stripping the
// component prefixes from the original environment variable names, replacing
// the general, non-prefixed ones.
func EnvForComponent(component string) *envSpec {
	return &envSpec{componentPrefix: fmt.Sprintf("%s_", strings.ToUpper(component))}
}

// Set the PATH prefix to be prepended in front of the original PATH. If the
// PATH is empty or not defined in the original environment, PATH will be set to
// this prefix. Setting the prefix to the empty string will disable this
// functionality.
func (s *envSpec) WithPathPrefix(pathPrefix string) *envSpec {
	s.pathPrefix = pathPrefix
	return s
}

// Won't strip the component prefix from variable names. Use for components that
// define their own prefix convention, such as ETCD_XXX. Note that the HTTP
// proxy variables will be rewritten even when the prefixes are to be kept.
func (s *envSpec) KeepEnvPrefix() *envSpec {
	s.keepEnvPrefix = true
	return s
}

// Build the process environment based on the given original environment.
func (s *envSpec) Build(env []string) []string {

	type envVarValue = struct {
		fromPrefixed bool   // indicates if the value originates from a prefixed variable
		value        string // the actual value of the variable
	}

	// Rewrite the input environment, i.e. strip
	// the component prefix from variable names.
	vars := make(map[string]envVarValue, len(env))
	for _, v := range env {
		name, value, valid := strings.Cut(v, "=")
		// Environment variables without an equals sign are malformed. Go strips
		// those in os.Environ() for UNIX-like operating systems anyways. Not
		// sure about Windows's GetEnvironmentStrings Win32 API call.
		// Nevertheless, makes sense to keep the behavior in sync between OSes.
		// The reverse operation when constructing the resulting environment
		// variable strings form an envVar will add an equals sign in any case.
		if !valid {
			continue
		}

		// Does this variable have the component prefix?
		isPrefixed := strings.HasPrefix(name, s.componentPrefix)
		if isPrefixed {
			// This is the non-prefixed target name.
			targetName := name[len(s.componentPrefix):]

			// Are the prefixes to be kept?
			if s.keepEnvPrefix {
				// Rename these variables even if the prefix is to be kept.
				// All other variables are not renamed and used as is.
				switch targetName {
				case "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY":
					name = targetName
				}
			} else {
				// Rename the variable to its shortened form.
				name = targetName
			}
		}

		// Only replace generic values with prefixed ones, nothing else.
		if prevValue, exists := vars[name]; !exists || (isPrefixed && !prevValue.fromPrefixed) {
			vars[name] = envVarValue{isPrefixed, value}
		}
	}

	// Prepend the PATH prefix.
	if s.pathPrefix != "" {
		newPath := s.pathPrefix
		if path, exists := vars["PATH"]; exists && path.value != "" {
			newPath = newPath + string(os.PathListSeparator) + path.value
		}
		vars["PATH"] = envVarValue{value: newPath}
	}

	// Construct the resulting environment variables.
	env = make([]string, 0, len(vars))
	for name, v := range vars {
		env = append(env, name+"="+v.value)
	}

	return env
}
