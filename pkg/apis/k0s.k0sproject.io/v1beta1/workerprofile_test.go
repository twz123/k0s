/*
Copyright 2022 k0s authors

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
package v1beta1

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWorkerProfile worker profile test suite
func TestWorkerProfile(t *testing.T) {
	t.Run("worker_profile_validation", func(t *testing.T) {
		cases := []struct {
			name   string
			spec   interface{}
			errMsg string
		}{
			{
				name:   "Generic spec is valid",
				spec:   map[string]interface{}{},
				errMsg: "",
			},
			{
				name:   "invalid YAML",
				spec:   []byte("kaput"),
				errMsg: "failed to parse worker profile",
			},
			{
				name: "Unknown fields are rejected",
				spec: map[string]interface{}{
					"foo": "bar",
				},
				errMsg: "unknown field \"foo\"",
			},
			{
				name: "Locked field clusterDNS",
				spec: map[string]interface{}{
					"clusterDNS": []string{"8.8.8.8"},
				},
				errMsg: "field `clusterDNS` is prohibited",
			},
			{
				name: "Locked field clusterDomain",
				spec: map[string]interface{}{
					"clusterDomain": "cluster.org",
				},
				errMsg: "field `clusterDomain` is prohibited",
			},
			{
				name: "Locked field apiVersion",
				spec: map[string]interface{}{
					"apiVersion": "v2",
				},
				errMsg: "field `apiVersion` is prohibited",
			},
			{
				name: "Locked field kind",
				spec: map[string]interface{}{
					"kind": "Controller",
				},
				errMsg: "field `kind` is prohibited",
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				var b []byte
				var err error
				switch tc.spec.(type) {
				case []byte:
					b = tc.spec.([]byte)
				default:
					b, err = json.Marshal(tc.spec)
					require.NoError(t, err)
				}
				profile := WorkerProfile{Config: b}
				err = profile.Validate()
				if tc.errMsg == "" {
					assert.NoError(t, err)
				} else {
					assert.Contains(t, err.Error(), tc.errMsg)
				}
			})
		}
	})
}
