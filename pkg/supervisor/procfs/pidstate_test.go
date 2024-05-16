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

package procfs_test

import (
	"fmt"
	"testing"

	"github.com/k0sproject/k0s/pkg/supervisor/procfs"
	"github.com/stretchr/testify/assert"
)

func TestPIDState_Format(t *testing.T) {
	for _, test := range []struct {
		ch   byte
		name string
	}{
		{'R', "running"},
		{'S', "sleeping"},
		{'D', "disk sleep"},
		{'T', "stopped"},
		{'t', "tracing stop"},
		{'Z', "zombie"},
		{'X', "dead"},
		{'x', "dead"},
		{'K', "wakekill"},
		{'W', "waking"},
		{'P', "parked"},
		{'I', "idle"},
		{'-', "??? (-)"}, // not a real thing
	} {
		s := procfs.PIDState(test.ch)
		assert.Equal(t, test.name, s.String(), "For String()")
		assert.Equal(t, test.name, fmt.Sprintf("%s", s), "For format %%s")
		assert.Equal(t, fmt.Sprintf("%c (%s)", test.ch, test.name), fmt.Sprintf("%v", s), "For format %%v")
		assert.Equal(t, fmt.Sprintf("PIDState(%q)", test.ch), fmt.Sprintf("%#v", s), "For format %%#v")
	}

	for _, test := range []struct{ expected, format string }{
		{"   running", "%10s"},
		{"runn", "%.4s"},
		{"running   ", "%-10s"},
		{"82", "%d"},
		{"+82", "%+d"},
		{" 82", "% d"},
		{"0000000082", "%010d"},
	} {
		assert.Equal(t, test.expected, fmt.Sprintf(test.format, procfs.PIDStateRunning), "For format %s", test.format)
	}

	assert.Equal(t, "%!f(PIDState=R (running))", fmt.Sprintf("%f", procfs.PIDStateRunning), "For bad format f")
}
