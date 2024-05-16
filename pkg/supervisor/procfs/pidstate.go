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

package procfs

import (
	"fmt"
	"strconv"
	"strings"
)

type PIDState byte

// Known values of the process state values as used in the third field of /proc/pid/stat.
const (
	PIDStateRunning     PIDState = 'R'
	PIDStateSleeping    PIDState = 'S' // in an interruptible wait
	PIDStateWaiting     PIDState = 'D' // in uninterruptible disk sleep
	PIDStateZombie      PIDState = 'Z'
	PIDStateStopped     PIDState = 'T' // (on a signal) or (before Linux 2.6.33) trace stopped
	PIDStateTracingStop PIDState = 't' // (Linux 2.6.33 onward)
	PIDStatePaging      PIDState = 'W' // (only before Linux 2.6.0)
	PIDStateDead        PIDState = 'X' // (from Linux 2.6.0 onward)
	PIDStateDeadX       PIDState = 'x' // (Linux 2.6.33 to 3.13 only)
	PIDStateWakekill    PIDState = 'K' // (Linux 2.6.33 to 3.13 only)
	PIDStateWaking      PIDState = 'W' // (Linux 2.6.33 to 3.13 only)
	PIDStateParked      PIDState = 'P' // (Linux 3.9 to 3.13 only)
	PIDStateIdle        PIDState = 'I' // (Linux 4.14 onward)
)

func (s PIDState) String() string {
	switch s {
	case PIDStateRunning:
		return "running"
	case PIDStateSleeping:
		return "sleeping"
	case PIDStateWaiting:
		return "disk sleep"
	case PIDStateZombie:
		return "zombie"
	case PIDStateStopped:
		return "stopped"
	case PIDStateTracingStop:
		return "tracing stop"
	case PIDStateDead, PIDStateDeadX:
		return "dead"
	case PIDStateWakekill:
		return "wakekill"
	case PIDStateWaking:
		return "waking"
	case PIDStateParked:
		return "parked"
	case PIDStateIdle:
		return "idle"
	default:
		return fmt.Sprintf("??? (%c)", byte(s))
	}
}

// Format implements the [fmt.Formatter] interface.
func (s PIDState) Format(f fmt.State, c rune) {
	switch c {
	case 'v':
		if f.Flag('#') {
			fmt.Fprintf(f, "PIDState(%q)", byte(s))
			return
		}
		fmt.Fprintf(f, "%c (%s)", byte(s), s.String())

	case 's':
		printf(f, c, s.String())

	// integer formats
	case 'b', 'c', 'd', 'o', 'O', 'q', 'x', 'X', 'U':
		printf(f, c, byte(s))

	default:
		fmt.Fprintf(f, "%%!%c(PIDState=%v)", c, s)
	}
}

func printf(f fmt.State, c rune, val any) {
	if formatter, ok := val.(fmt.Formatter); ok {
		formatter.Format(f, c)
		return
	}

	var spec strings.Builder
	spec.WriteString("%")
	for _, flag := range "+-# 0" {
		if f.Flag(int(flag)) {
			spec.WriteRune(flag)
		}
	}
	if width, ok := f.Width(); ok {
		spec.WriteString(strconv.Itoa(width))
	}
	if precision, ok := f.Precision(); ok {
		spec.WriteRune('.')
		if precision != 0 {
			spec.WriteString(strconv.Itoa(precision))
		}
	}
	spec.WriteRune(c)
	fmt.Fprintf(f, spec.String(), val)
}
