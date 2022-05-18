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
package controller

import (
	"context"

	"github.com/k0sproject/k0s/pkg/component"
)

func NoOpLeaderElector() LeaderElector {
	return &noOpLeader{}
}

func SingleLeader() interface {
	LeaderElector
	component.Component
} {
	return &singleLeader{}
}

type noOpLeader struct{}

func (*noOpLeader) IsLeader() bool                  { return false }
func (*noOpLeader) AddAcquiredLeaseCallback(func()) {}
func (*noOpLeader) AddLostLeaseCallback(func())     {}

type singleLeader struct {
	noOpLeader
	component.NoOpComponent

	callbacks []func()
}

func (*singleLeader) IsLeader() bool { return true }

func (l *singleLeader) AddAcquiredLeaseCallback(fn func()) {
	if fn != nil {
		l.callbacks = append(l.callbacks, fn)
	}
}

func (l *singleLeader) Run(_ context.Context) error {
	for _, fn := range l.callbacks {
		fn()
	}
	return nil
}
