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
	"errors"
	"sync"
	"sync/atomic"
)

type DummyLeaderElector struct {
	Leader bool

	mu        sync.Mutex
	state     uint32
	callbacks []func()
}

const (
	dummyLeaderElectorCreated uint32 = iota
	dummyLeaderElectorRunning
	dummyLeaderElectorStopped
)

func (l *DummyLeaderElector) IsLeader() bool { return l.Leader }

func (l *DummyLeaderElector) AddAcquiredLeaseCallback(fn func()) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if atomic.LoadUint32(&l.state) == dummyLeaderElectorRunning {
		fn()
	} else {
		l.callbacks = append(l.callbacks, fn)
	}
}

func (l *DummyLeaderElector) AddLostLeaseCallback(func()) {}

func (l *DummyLeaderElector) Run(context.Context) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if !atomic.CompareAndSwapUint32(&l.state, dummyLeaderElectorCreated, dummyLeaderElectorRunning) {
		return errors.New("cannot run")
	}

	if !l.Leader {
		return nil
	}
	for _, fn := range l.callbacks {
		if fn != nil {
			fn()
		}
	}

	return nil
}

func (l *DummyLeaderElector) Init(context.Context) error { return nil }

func (l *DummyLeaderElector) Healthy() error {
	if atomic.LoadUint32(&l.state) != dummyLeaderElectorRunning {
		return errors.New("not running")
	}

	return nil
}

func (l *DummyLeaderElector) Stop() error {
	l.mu.Lock()
	atomic.StoreUint32(&l.state, dummyLeaderElectorStopped)
	l.mu.Unlock()
	return nil
}
