// Copyright 2025 k0s authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package supervised

import (
	"fmt"

	"github.com/sirupsen/logrus"
	"golang.org/x/sys/windows/svc/eventlog"
)

type EventLog interface {
	Info(id InfoEventID, args ...any)
	Error(id ErrorEventID, args ...any)
}

// Event IDs for k0s.
// https://learn.microsoft.com/en-us/windows/win32/eventlog/event-identifiers
const (

	// (Facility is 2835)
	// EID_K0S_SUC_0 = 0x2b130000
	// EID_K0S_INF_0 = 0x6b130000
	// EID_K0S_WRN_0 = 0xab130000
	// EID_K0S_ERR_0 = 0xeb130000

	// EID_K0S_INF_0 = 0x40000000
	// EID_K0S_ERR_0 = 0xc0000000

	EID_K0S_INF_0 = 10
	EID_K0S_ERR_0 = 0
)

type InfoEventID uint16

func (id InfoEventID) EventID() uint32 { return EID_K0S_INF_0 + uint32(id) }

type ErrorEventID uint16

func (id ErrorEventID) EventID() uint32 { return EID_K0S_ERR_0 + uint32(id) }

type eventLog struct {
	serviceName string
	log         *eventlog.Log
}

var _ EventLog = (*eventLog)(nil)

func (e *eventLog) Info(id InfoEventID, args ...any) {
	logrus.WithFields(logrus.Fields{
		"component": "eventlog",
		"service":   e.serviceName,
		"eventId":   id.EventID(),
	}).Info(args...)
	e.log.Info(id.EventID(), fmt.Sprint(args...))
}

func (e *eventLog) Error(id ErrorEventID, args ...any) {
	logrus.WithFields(logrus.Fields{
		"component": "eventlog",
		"service":   e.serviceName,
		"eventId":   id.EventID(),
	}).Error(args...)
	e.log.Error(id.EventID(), fmt.Sprint(args...))
}
