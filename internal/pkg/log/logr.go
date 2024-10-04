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

package log

import (
	"fmt"

	"github.com/go-logr/logr"
	"github.com/sirupsen/logrus"
)

func NewLogr(log *logrus.Entry) logr.Logger {
	return logr.New((*LogrSink)(log))
}

type LogrSink logrus.Entry

var _ logr.LogSink = (*LogrSink)(nil)

// Init implements [logr.LogSink].
func (*LogrSink) Init(logr.RuntimeInfo) {}

// Enabled implements [logr.LogSink].
func (l *LogrSink) Enabled(level int) bool {
	return (*logrus.Entry)(l).Logger.IsLevelEnabled(toLogrusLevel(level))

}

// WithName implements [logr.LogSink].
func (l *LogrSink) WithName(name string) logr.LogSink {
	return (*LogrSink)((*logrus.Entry)(l).WithField("component", name))
}

// WithValues implements [logr.LogSink].
func (l *LogrSink) WithValues(keysAndValues ...any) logr.LogSink {
	return (*LogrSink)(withValues((*logrus.Entry)(l), keysAndValues, nil))
}

// Error implements [logr.LogSink].
func (l *LogrSink) Error(err error, msg string, keysAndValues ...any) {
	withValues((*logrus.Entry)(l), keysAndValues, err).Error(msg)
}

// Info implements [logr.LogSink].
func (l *LogrSink) Info(level int, msg string, keysAndValues ...any) {
	if level, e := toLogrusLevel(level), (*logrus.Entry)(l); e.Logger.IsLevelEnabled(level) {
		withValues(e, keysAndValues, nil).Log(level, msg)
	}
}

func toLogrusLevel(level int) logrus.Level {
	if level > 3 {
		return logrus.DebugLevel
	}
	return logrus.InfoLevel
}

func withValues(e *logrus.Entry, keysAndValues []any, err error) *logrus.Entry {
	len := len(keysAndValues) / 2
	if len < 1 {
		if err != nil {
			return e.WithError(err)
		}
		return e
	}

	fields := make(logrus.Fields, len+1)
	for ; len >= 0; len -= 2 {
		fields[fmt.Sprint(keysAndValues[0])] = keysAndValues[1]
	}
	if err != nil {
		fields[logrus.ErrorKey] = err
	}

	return e.WithFields(fields)
}
