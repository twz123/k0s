// SPDX-FileCopyrightText: 2020 k0s authors
// SPDX-License-Identifier: Apache-2.0

package v1beta1

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/runtime"
)

// TestWorkerProfile worker profile test suite
func TestWorkerProfile(t *testing.T) {
	t.Run("worker_profile_validation", func(t *testing.T) {
		cases := []struct {
			name  string
			spec  map[string]interface{}
			valid bool
		}{
			{
				name:  "Generic spec is valid",
				spec:  map[string]interface{}{},
				valid: true,
			},
			{
				name: "Locked field clusterDNS",
				spec: map[string]interface{}{
					"clusterDNS": "8.8.8.8",
				},
				valid: false,
			},
			{
				name: "Locked field clusterDomain",
				spec: map[string]interface{}{
					"clusterDomain": "cluster.org",
				},
				valid: false,
			},
			{
				name: "Locked field apiVersion",
				spec: map[string]interface{}{
					"apiVersion": "v2",
				},
				valid: false,
			},
			{
				name: "Locked field kind",
				spec: map[string]interface{}{
					"kind": "Controller",
				},
				valid: false,
			}, {
				name: "Locked field staticPodURL",
				spec: map[string]interface{}{
					"staticPodURL": "foo",
				},
				valid: false,
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				value, err := json.Marshal(tc.spec)
				if err != nil {
					t.Fatal(err)
				}
				profile := WorkerProfile{Config: &runtime.RawExtension{Raw: value}}
				valid := profile.Validate() == nil
				assert.Equal(t, valid, tc.valid)
			})
		}
	})
}
